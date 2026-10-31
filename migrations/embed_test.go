package migrations

import "testing"

func TestBundledMigrationVersions(t *testing.T) {
	versions, err := Versions()
	if err != nil || len(versions) < 2 || versions[0] != 1 {
		t.Fatalf("versions=%v error=%v", versions, err)
	}
	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Fatal("migration versions must be strictly increasing")
		}
	}
}
