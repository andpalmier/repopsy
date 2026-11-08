package git

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestTrackString(t *testing.T) {
	b := newRepo(t)
	b.Write("a.txt", "nothing yet\n", 0o644)
	b.Commit("start")
	b.Write("a.txt", "call(x) once\n", 0o644)
	first := b.Commit("introduce")
	b.Write("a.txt", "call(x) call(x)\n", 0o644)
	b.Write("b.bin", "\x00call(x)\x00", 0o644)
	b.Commit("spread")
	b.Write("unrelated.txt", "no match\n", 0o644)
	b.Commit("unrelated")
	main := b.Branch()
	b.Git("checkout", "-q", "-b", "cleanup")
	b.Write("a.txt", "gone\n", 0o644)
	b.Commit("remove")

	// "(x)" would be a group to a regex; it must be matched literally.
	h, err := b.open().TrackString(context.Background(), "call(x)", []TrackRef{
		{Label: main, Rev: "refs/heads/" + main},
		{Label: "cleanup", Rev: "refs/heads/cleanup"},
	})
	if err != nil {
		t.Fatalf("TrackString: %v", err)
	}

	c, ok := h.First()
	if !ok || c.Hash != first || c.Delta() != 1 {
		t.Errorf("First = %+v, %v", c, ok)
	}
	var summary []string
	for _, c := range h.Changes {
		var paths []string
		for _, p := range c.Paths {
			paths = append(paths, p.Path+":"+strconv.Itoa(p.Before)+">"+strconv.Itoa(p.After))
		}
		summary = append(summary, c.Subject+"["+strings.Join(paths, ",")+"]")
	}
	want := "introduce[a.txt:0>1] spread[a.txt:1>2,b.bin:0>1] remove[a.txt:2>0]"
	if got := strings.Join(summary, " "); got != want {
		t.Errorf("changes = %s\nwant      %s", got, want)
	}

	if len(h.Tips) != 2 || h.Tips[0].Total != 3 || h.Tips[1].Total != 1 {
		t.Fatalf("tips = %+v", h.Tips)
	}
	if h.Tips[1].Paths[0].Path != "b.bin" {
		t.Errorf("cleanup tip paths = %+v", h.Tips[1].Paths)
	}

	none, err := b.open().TrackString(context.Background(), "never-there", []TrackRef{{Label: main, Rev: main}})
	if err != nil || len(none.Changes) != 0 || none.Tips[0].Total != 0 {
		t.Errorf("absent string: %+v, %v", none, err)
	}
	if _, ok := none.First(); ok {
		t.Error("an absent string has a first appearance")
	}

	if _, err := b.open().TrackString(context.Background(), "two\nlines", nil); err == nil {
		t.Error("a multi-line string was accepted")
	}
}
