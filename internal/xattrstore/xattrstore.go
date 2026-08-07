// Package xattrstore maps SMB-visible extended attribute names to the native
// names used by the backend filesystem.
package xattrstore

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/pkg/xattr"
)

const (
	linuxPrefix     = "user.smbrelay."
	linuxMaxNameLen = 255
)

// NativeName returns the host-filesystem name used to store a logical SMB
// extended attribute. Linux only permits application attributes in user.*;
// the private prefix also prevents native security/system attributes from
// leaking into SMB named-stream enumeration.
func NativeName(name string) (string, error) {
	return nativeNameForOS(runtime.GOOS, name)
}

func nativeNameForOS(goos, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("extended attribute name is empty")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("extended attribute name contains NUL")
	}
	if goos != "linux" {
		return name, nil
	}
	native := linuxPrefix + name
	if len(native) > linuxMaxNameLen {
		return "", fmt.Errorf("extended attribute name is too long")
	}
	return native, nil
}

func logicalNameForOS(goos, native string) (string, bool) {
	if goos != "linux" {
		return native, true
	}
	if !strings.HasPrefix(native, linuxPrefix) {
		return "", false
	}
	logical := strings.TrimPrefix(native, linuxPrefix)
	return logical, logical != ""
}

func Get(path, name string) ([]byte, error) {
	native, err := NativeName(name)
	if err != nil {
		return nil, err
	}
	return xattr.Get(path, native)
}

func FGet(file *os.File, name string) ([]byte, error) {
	native, err := NativeName(name)
	if err != nil {
		return nil, err
	}
	return xattr.FGet(file, native)
}

func Set(path, name string, value []byte) error {
	native, err := NativeName(name)
	if err != nil {
		return err
	}
	return xattr.Set(path, native, value)
}

func FSet(file *os.File, name string, value []byte) error {
	native, err := NativeName(name)
	if err != nil {
		return err
	}
	return xattr.FSet(file, native, value)
}

func Remove(path, name string) error {
	native, err := NativeName(name)
	if err != nil {
		return err
	}
	return xattr.Remove(path, native)
}

func FRemove(file *os.File, name string) error {
	native, err := NativeName(name)
	if err != nil {
		return err
	}
	return xattr.FRemove(file, native)
}

func List(path string) ([]string, error) {
	names, err := xattr.List(path)
	if err != nil {
		return nil, err
	}
	return logicalNames(runtime.GOOS, names), nil
}

func FList(file *os.File) ([]string, error) {
	names, err := xattr.FList(file)
	if err != nil {
		return nil, err
	}
	return logicalNames(runtime.GOOS, names), nil
}

func logicalNames(goos string, native []string) []string {
	seen := make(map[string]struct{}, len(native))
	logical := make([]string, 0, len(native))
	for _, name := range native {
		name, visible := logicalNameForOS(goos, name)
		if !visible {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		logical = append(logical, name)
	}
	sort.Strings(logical)
	return logical
}
