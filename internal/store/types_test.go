package store

import (
	"encoding/json"
	"testing"

	"github.com/yasyf/cc-interact/vcs"
)

func TestVersionFileFlags(t *testing.T) {
	classified := []ClassifiedFile{
		{FileChange: vcs.FileChange{Path: "a.pb.go", Status: "A", Fingerprint: "f1"}, Generated: true},
		{FileChange: vcs.FileChange{Path: "vendor/x.go", Status: "M", Fingerprint: "f2"}, Vendored: true},
		{FileChange: vcs.FileChange{Path: "main.go", Status: "A", Fingerprint: "f3"}},
	}
	raw, err := json.Marshal(classified)
	if err != nil {
		t.Fatal(err)
	}
	v := Version{ID: 7, FilesJSON: string(raw)}

	t.Run("Files decodes the base subset, ignoring the extra keys", func(t *testing.T) {
		files, err := v.Files()
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 3 {
			t.Fatalf("Files() len = %d, want 3", len(files))
		}
		if files[0].Path != "a.pb.go" || files[0].Status != "A" || files[0].Fingerprint != "f1" {
			t.Errorf("Files()[0] = %+v, want path/status/fingerprint preserved", files[0])
		}
	})

	t.Run("FileFlags reads the generated/vendored extras", func(t *testing.T) {
		flags, err := v.FileFlags()
		if err != nil {
			t.Fatal(err)
		}
		if len(flags) != 3 {
			t.Fatalf("FileFlags() len = %d, want 3", len(flags))
		}
		if flags[0].Path != "a.pb.go" || !flags[0].Generated || flags[0].Vendored {
			t.Errorf("FileFlags()[0] = %+v, want a.pb.go generated only", flags[0])
		}
		if flags[1].Path != "vendor/x.go" || flags[1].Generated || !flags[1].Vendored {
			t.Errorf("FileFlags()[1] = %+v, want vendor/x.go vendored only", flags[1])
		}
		if flags[2].Path != "main.go" || flags[2].Generated || flags[2].Vendored {
			t.Errorf("FileFlags()[2] = %+v, want main.go with no flags", flags[2])
		}
	})

	t.Run("a key-less files_json yields zero flags", func(t *testing.T) {
		plain, err := json.Marshal([]vcs.FileChange{{Path: "old.go", Status: "A", Fingerprint: "f0"}})
		if err != nil {
			t.Fatal(err)
		}
		v := Version{ID: 8, FilesJSON: string(plain)}
		flags, err := v.FileFlags()
		if err != nil {
			t.Fatal(err)
		}
		if len(flags) != 1 || flags[0].Path != "old.go" || flags[0].Generated || flags[0].Vendored {
			t.Errorf("FileFlags() = %+v, want a single old.go with zero flags", flags)
		}
	})

	t.Run("omitempty keeps both-false bytes identical to the plain encoding", func(t *testing.T) {
		fc := vcs.FileChange{Path: "main.go", Status: "A", Fingerprint: "f3"}
		plain, _ := json.Marshal(fc)
		wrapped, _ := json.Marshal(ClassifiedFile{FileChange: fc})
		if string(plain) != string(wrapped) {
			t.Errorf("encodings differ:\n plain   = %s\n wrapped = %s", plain, wrapped)
		}
	})
}
