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
    let name = if cfg!(windows) { "re-psn-app.exe" } else { "re-psn-app" };
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

fn stop(mut b: Backend) {
    drop(b.stdin.take());
    let deadline = Instant::now() + STOP_TIMEOUT;
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

fn start() -> Result<(Backend, String, Arc<AtomicBool>), String> {
    let dir = private_dir()?;
    let file = dir.join("link.txt");
    let mut child = Command::new(app_binary()?)
        .args(["ui", "--no-open", "--exit-with-parent", "--url-file"])
        .arg(&file)
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
    let link = read_link(&file, &mut child);
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
    let state = State { backend: Mutex::new(Some(backend)), reopened };
    tauri::Builder::default()
        .manage(state)
        .setup(move |app| {
            let url = WebviewUrl::External(link.parse().map_err(|_| "invalid launch link")?);
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
