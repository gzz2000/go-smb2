package xattrstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLinuxNameMapping(t *testing.T) {
	for _, logical := range []string{"AFP_AfpInfo", "com.apple.lastuseddate#PS", "AFP_Resource"} {
		native, err := nativeNameForOS("linux", logical)
		if err != nil {
			t.Fatalf("map %q: %v", logical, err)
		}
		if native != linuxPrefix+logical {
			t.Fatalf("native name = %q, want %q", native, linuxPrefix+logical)
		}
		got, visible := logicalNameForOS("linux", native)
		if !visible || got != logical {
			t.Fatalf("logical name = %q, %v; want %q, true", got, visible, logical)
		}
	}
}

func TestLinuxEnumerationHidesNativeAttributes(t *testing.T) {
	native := []string{
		"security.selinux",
		"system.posix_acl_access",
		"user.unrelated",
		linuxPrefix + "AFP_AfpInfo",
		linuxPrefix + "com.apple.lastuseddate#PS",
	}
	want := []string{"AFP_AfpInfo", "com.apple.lastuseddate#PS"}
	if got := logicalNames("linux", native); !reflect.DeepEqual(got, want) {
		t.Fatalf("logical names = %#v, want %#v", got, want)
	}
}

func TestNameValidation(t *testing.T) {
	if _, err := nativeNameForOS("linux", ""); err == nil {
		t.Fatal("empty name was accepted")
	}
	if _, err := nativeNameForOS("linux", "bad\x00name"); err == nil {
		t.Fatal("name containing NUL was accepted")
	}
	if _, err := nativeNameForOS("linux", strings.Repeat("x", linuxMaxNameLen-len(linuxPrefix)+1)); err == nil {
		t.Fatal("overlong Linux name was accepted")
	}
}

func TestFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	const name = "com.apple.lastuseddate#PS"
	want := []byte("0123456789abcdef")
	if err := FSet(file, name, want); err != nil {
		t.Fatal(err)
	}
	got, err := FGet(file, name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("value = %x, want %x", got, want)
	}
	names, err := FList(file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{name}) {
		t.Fatalf("names = %#v, want %#v", names, []string{name})
	}
	if err := FRemove(file, name); err != nil {
		t.Fatal(err)
	}
}
