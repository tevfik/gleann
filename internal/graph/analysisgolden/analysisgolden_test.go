package analysisgolden

import "testing"

// A Windows index directory is written to the goldens' form however the
// output carries it: native, JSON-escaped or with forward slashes.
func TestNormalizeWindowsPaths(t *testing.T) {
	e := &Env{IndexDir: `C:\Temp\T1\001`, Fixture: `C:\src\fixture`}
	for in, want := range map[string]string{
		`"db_path": "C:\\Temp\\T1\\001\\fixture\\fixture.db",`: `"db_path": "<INDEXDIR>/fixture/fixture.db",`,
		`open C:\Temp\T1\001\fixture\fixture.db: x`:            `open <INDEXDIR>/fixture/fixture.db: x`,
		`file C:/src/fixture/service/service.go`:               `file <FIXTURE>/service/service.go`,
		`unrelated C:\Other\path`:                              `unrelated C:\Other\path`,
	} {
		if got := e.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	unix := &Env{IndexDir: "/tmp/T1/001", Fixture: "/src/fixture"}
	if got, want := unix.Normalize(`"db_path": "/tmp/T1/001/fixture/fixture.db"`), `"db_path": "<INDEXDIR>/fixture/fixture.db"`; got != want {
		t.Errorf("unix: %q, want %q", got, want)
	}
}
