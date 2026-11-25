package backend

import "testing"

func TestRenameSession(t *testing.T) {
	s := &Service{dataDir: t.TempDir()}
	if err := s.RenameSession("a", "  Trip   plan "); err != nil {
		t.Fatal(err)
	}
	if got := readTitles(s.dataDir)["a"]; got != "Trip plan" {
		t.Fatalf("title = %q", got)
	}
	if err := s.RenameSession("a", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := readTitles(s.dataDir)["a"]; ok {
		t.Fatal("empty name should clear the title")
	}
	if err := s.RenameSession(" ", "x"); err == nil {
		t.Fatal("blank id should fail")
	}
}
