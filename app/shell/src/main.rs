//! Native window for the re-psn desktop app. It starts `re-psn-app` (the Go
//! program that serves the interface on a private loopback address), shows the
//! one-use launch link in a window, and stops the app when the window closes.
//! The shell holds no settings, profile or credentials of its own.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::{
    fs,
    io::{BufRead, BufReader},
    path::{Path, PathBuf},
    process::{Child, ChildStdin, Command, Stdio},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder, WindowEvent};

/// How long the app has to publish its launch link.
const START_TIMEOUT: Duration = Duration::from_secs(20);
/// How long the app has to stop after its pipe closes before it is killed.
const STOP_TIMEOUT: Duration = Duration::from_secs(8);

struct Backend {
    child: Child,
    /// Dropping this closes the app's standard input, which asks it to stop.
    stdin: Option<ChildStdin>,
}

struct State {
    backend: Mutex<Option<Backend>>,
    /// True when another running app took over the link and this shell owns nothing.
    reopened: Arc<AtomicBool>,
}

fn app_binary() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| e.to_string())?;
    let name = if cfg!(windows) {
        "re-psn-app.exe"
    } else {
        "re-psn-app"
    };
    let path = exe.parent().ok_or("no program folder")?.join(name);
    if path.is_file() {
        Ok(path)
    } else {
        Err(format!("{name} was not found next to this program"))
    }
}

/// Creates a new private folder for the launch-link file.
fn private_dir() -> Result<PathBuf, String> {
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|e| e.to_string())?
        .subsec_nanos();
    let dir = std::env::temp_dir().join(format!("re-psn-shell-{}-{nanos}", std::process::id()));
    // Only the Unix branch below changes the builder.
    #[cfg_attr(not(unix), allow(unused_mut))]
    let mut b = fs::DirBuilder::new();
    #[cfg(unix)]
    std::os::unix::fs::DirBuilderExt::mode(&mut b, 0o700);
    b.create(&dir).map_err(|e| e.to_string())?;
    Ok(dir)
}

fn read_link(file: &Path, child: &mut Child) -> Result<String, String> {
    let deadline = Instant::now() + START_TIMEOUT;
    loop {
        if let Ok(s) = fs::read_to_string(file) {
            if s.ends_with('\n') {
                return Ok(s.trim().to_string());
            }
        }
        if let Ok(Some(status)) = child.try_wait() {
            return Err(format!("the app stopped before it was ready ({status})"));
        }
        if Instant::now() > deadline {
            return Err("the app did not become ready in time".into());
        }
        std::thread::sleep(Duration::from_millis(50));
    }
}

/// The window may only show the app's own loopback page. The app is trusted to print a
/// link like that, but the window checks it before it loads anything from it.
fn check_link(link: &str) -> Result<tauri::Url, String> {
    let url: tauri::Url = link
        .parse()
        .map_err(|_| "the app printed an invalid launch link".to_string())?;
    let local = matches!(url.host_str(), Some("127.0.0.1") | Some("localhost"));
    if url.scheme() != "http" || !local || !url.username().is_empty() || url.password().is_some() {
        return Err("the app printed a launch link that is not a plain local http address".into());
    }
    Ok(url)
}

fn stop(b: Backend) {
    stop_within(b, STOP_TIMEOUT)
}

fn stop_within(mut b: Backend, timeout: Duration) {
    drop(b.stdin.take());
    let deadline = Instant::now() + timeout;
    loop {
        match b.child.try_wait() {
            Ok(Some(_)) => return,
            _ if Instant::now() > deadline => break,
            _ => std::thread::sleep(Duration::from_millis(50)),
        }
    }
    let _ = b.child.kill();
    let _ = b.child.wait();
}

/// The command line for the app: the caller's own `re-psn-app ui` flags (a settings file or
/// profile folder, say) first, then the flags this window needs, which win over any repeat.
fn app_args(user: &[String], link_file: &Path) -> Vec<std::ffi::OsString> {
    let mut args: Vec<std::ffi::OsString> = vec!["ui".into()];
    args.extend(user.iter().map(Into::into));
    args.extend([
        "--no-open".into(),
        "--exit-with-parent".into(),
        "--url-file".into(),
        link_file.into(),
    ]);
    args
}

fn start() -> Result<(Backend, tauri::Url, Arc<AtomicBool>), String> {
    let dir = private_dir()?;
    let file = dir.join("link.txt");
    let mut child = Command::new(app_binary()?)
        .args(app_args(
            &std::env::args().skip(1).collect::<Vec<_>>(),
            &file,
        ))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|e| format!("could not start the app: {e}"))?;
    let stdin = child.stdin.take();
    let reopened = Arc::new(AtomicBool::new(false));
    if let Some(out) = child.stdout.take() {
        let flag = reopened.clone();
        std::thread::spawn(move || {
            for line in BufReader::new(out).lines().map_while(Result::ok) {
                if line.starts_with("Opened the existing app") {
                    flag.store(true, Ordering::SeqCst);
                }
            }
        });
    }
    let link = read_link(&file, &mut child).and_then(|l| check_link(&l));
    let _ = fs::remove_dir_all(&dir);
    match link {
        Ok(link) => Ok((Backend { child, stdin }, link, reopened)),
        Err(e) => {
            stop(Backend { child, stdin });
            Err(e)
        }
    }
}

fn main() {
    let (backend, link, reopened) = match start() {
        Ok(v) => v,
        Err(e) => {
            eprintln!("re-psn: {e}");
            std::process::exit(1);
        }
    };
    let state = State {
        backend: Mutex::new(Some(backend)),
        reopened,
    };
    tauri::Builder::default()
        .manage(state)
        .setup(move |app| {
            let url = WebviewUrl::External(link.clone());
            WebviewWindowBuilder::new(app, "main", url)
                .title("re:psn")
                .inner_size(1100.0, 780.0)
                .build()?;
            // The app can also stop on its own (its Stop app button): close the window then.
            let handle = app.handle().clone();
            std::thread::spawn(move || loop {
                std::thread::sleep(Duration::from_millis(300));
                let st = handle.state::<State>();
                let mut guard = st.backend.lock().unwrap();
                let gone = match guard.as_mut() {
                    Some(b) => matches!(b.child.try_wait(), Ok(Some(_))),
                    None => return,
                };
                // A second shell onto a running app exits at once with nothing to stop; keep its window.
                // Its note that it reopened may still be in the pipe, so give the reader a moment.
                if gone {
                    std::thread::sleep(Duration::from_millis(300));
                }
                if gone && !st.reopened.load(Ordering::SeqCst) {
                    drop(guard);
                    handle.exit(0);
                    return;
                }
                if gone {
                    return;
                }
            });
            Ok(())
        })
        .on_window_event(|w, ev| {
            if let WindowEvent::Destroyed = ev {
                let b = w.state::<State>().backend.lock().unwrap().take();
                if let Some(b) = b {
                    if w.state::<State>().reopened.load(Ordering::SeqCst) {
                        drop(b.stdin);
                    } else {
                        stop(b);
                    }
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("could not start the window");
}

#[cfg(test)]
mod tests {
    use super::*;

    fn quick_exit(code: i32) -> Child {
        let mut c = if cfg!(windows) {
            let mut c = Command::new("cmd");
            c.args(["/C", &format!("exit {code}")]);
            c
        } else {
            let mut c = Command::new("sh");
            c.args(["-c", &format!("exit {code}")]);
            c
        };
        c.spawn().unwrap()
    }

    fn long_running() -> Child {
        let mut c = if cfg!(windows) {
            let mut c = Command::new("cmd");
            c.args(["/C", "ping -n 30 127.0.0.1 >nul"]);
            c
        } else {
            let mut c = Command::new("sleep");
            c.arg("30");
            c
        };
        c.stdout(Stdio::null()).spawn().unwrap()
    }

    #[test]
    fn private_dir_is_new_each_time_and_private() {
        let a = private_dir().unwrap();
        let b = private_dir().unwrap();
        assert_ne!(a, b);
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            for d in [&a, &b] {
                assert_eq!(fs::metadata(d).unwrap().permissions().mode() & 0o077, 0);
            }
        }
        fs::remove_dir_all(a).unwrap();
        fs::remove_dir_all(b).unwrap();
    }

    #[test]
    fn read_link_waits_for_a_complete_line() {
        let dir = private_dir().unwrap();
        let file = dir.join("link.txt");
        fs::write(&file, "http://127.0.0.1:1/#token=ab").unwrap();
        let writer = file.clone();
        let t = std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(300));
            fs::write(&writer, "http://127.0.0.1:1/#token=abcd\n").unwrap();
        });
        let mut child = long_running();
        let link = read_link(&file, &mut child).unwrap();
        assert_eq!(link, "http://127.0.0.1:1/#token=abcd");
        let _ = child.kill();
        let _ = child.wait();
        t.join().unwrap();
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn read_link_reports_an_app_that_stopped_early() {
        let dir = private_dir().unwrap();
        let mut child = quick_exit(3);
        let err = read_link(&dir.join("never.txt"), &mut child).unwrap_err();
        assert!(err.contains("stopped before it was ready"), "{err}");
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn check_link_accepts_only_the_apps_local_page() {
        for ok in ["http://127.0.0.1:43241/#token=ab", "http://localhost:80/"] {
            assert!(check_link(ok).is_ok(), "{ok}");
        }
        for bad in [
            "https://127.0.0.1:43241/",
            "http://example.org/",
            "http://127.0.0.1.example.org/",
            "http://user@127.0.0.1:1/",
            "http://user:pw@localhost:1/",
            "file:///etc/passwd",
            "javascript:alert(1)",
            "not a link",
            "",
        ] {
            assert!(check_link(bad).is_err(), "{bad}");
        }
    }

    #[test]
    fn app_args_put_the_callers_flags_first_and_the_windows_last() {
        let a = app_args(
            &[
                "--config".into(),
                "/x/re-psn.ini".into(),
                "--no-open".into(),
            ],
            Path::new("/tmp/l.txt"),
        );
        let a: Vec<String> = a.iter().map(|s| s.to_string_lossy().into_owned()).collect();
        assert_eq!(
            a,
            [
                "ui",
                "--config",
                "/x/re-psn.ini",
                "--no-open",
                "--no-open",
                "--exit-with-parent",
                "--url-file",
                "/tmp/l.txt"
            ]
        );
        assert_eq!(app_args(&[], Path::new("/l")).len(), 5);
    }

    #[test]
    fn app_binary_must_be_beside_the_program() {
        // The test program's folder holds no re-psn-app.
        assert!(app_binary().is_err());
    }

    #[cfg(unix)]
    #[test]
    fn stop_closes_input_first_and_kills_only_if_needed() {
        // cat ends when its standard input closes: a clean stop well before the limit.
        let mut child = Command::new("cat")
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .spawn()
            .unwrap();
        let stdin = child.stdin.take();
        let t = Instant::now();
        stop_within(Backend { child, stdin }, Duration::from_secs(5));
        assert!(t.elapsed() < Duration::from_secs(3));
        // sleep ignores its input: it is killed after the limit.
        let mut child = Command::new("sleep")
            .arg("30")
            .stdin(Stdio::piped())
            .spawn()
            .unwrap();
        let stdin = child.stdin.take();
        let t = Instant::now();
        stop_within(Backend { child, stdin }, Duration::from_millis(300));
        assert!(t.elapsed() < Duration::from_secs(5));
    }
}
