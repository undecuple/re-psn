package main

import "testing"

// The member counter wraps after 65535 joins. A new member must never get an ID a current member holds (the owner's ID
// decides who may change or kick), and 0 stays unused.
func TestMemberIDWrapSkipsIDsInUse(t *testing.T) {
	r := &room{id: 1, max: 16}
	owner := &client{rooms: map[uint64]*room{}}
	om := (&Server{}).addMember(r, owner, nil)
	if om.id != 1 {
		t.Fatalf("first member id %d", om.id)
	}
	r.owner = om.id
	r.nextMember = 65534 // the next join gets 65535, the one after wraps
	seen := map[uint16]bool{om.id: true}
	for i := 0; i < 5; i++ {
		m := (&Server{}).addMember(r, &client{rooms: map[uint64]*room{}}, nil)
		if m.id == 0 || seen[m.id] {
			t.Fatalf("join %d got id %d (in use or zero)", i, m.id)
		}
		seen[m.id] = true
		if m.id == r.owner {
			t.Fatal("a joiner was given the owner's id")
		}
	}
	// A member who left frees its ID for reuse only after the counter comes round again; the owner's never repeats while held.
	for r.nextMember != 0 {
		r.nextMember++
	}
	m := (&Server{}).addMember(r, &client{rooms: map[uint64]*room{}}, nil)
	if m.id == om.id {
		t.Fatalf("wrapped counter reissued the owner's id %d", m.id)
	}
}
