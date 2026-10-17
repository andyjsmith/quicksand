package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func checkTree(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s/%s: %d bytes, %v; want %d bytes", root, name, len(got), err, len(want))
		}
	}
}

func TestUploadDownload(t *testing.T) {
	_, guest := startServer(t)
	local := t.TempDir()
	big := make([]byte, 5<<20)
	rand.Read(big)
	writeTree(t, local, map[string][]byte{"a.txt": []byte("alpha"), "big.bin": big})

	// Into the files directory, keeping names; then to a new name.
	if err := runQS("upload", "vm1", filepath.Join(local, "a.txt"), filepath.Join(local, "big.bin"), "."); err != nil {
		t.Fatal(err)
	}
	// Without a remote path, into the files directory.
	os.WriteFile(filepath.Join(local, "c.txt"), []byte("gamma"), 0o644)
	if err := runQS("upload", "vm1", filepath.Join(local, "c.txt")); err != nil {
		t.Fatal(err)
	}
	checkTree(t, guest, map[string][]byte{"c.txt": []byte("gamma")})
	if err := runQS("upload", "vm1", filepath.Join(local, "a.txt"), "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	checkTree(t, guest, map[string][]byte{"a.txt": []byte("alpha"), "big.bin": big, "renamed.txt": []byte("alpha")})
	// Overwrites, like scp.
	os.WriteFile(filepath.Join(local, "a.txt"), []byte("beta"), 0o644)
	if err := runQS("upload", "vm1", filepath.Join(local, "a.txt"), "a.txt"); err != nil {
		t.Fatal(err)
	}
	checkTree(t, guest, map[string][]byte{"a.txt": []byte("beta")})
	for _, args := range [][]string{
		{"vm1", filepath.Join(local, "a.txt"), filepath.Join(local, "big.bin"), "a.txt"}, // several into a file
		{"vm1", local, "."},                        // a directory
		{"vm1", filepath.Join(local, "nope"), "."}, // a missing file
	} {
		if err := runQS(append([]string{"upload"}, args...)...); err == nil {
			t.Errorf("upload %q succeeded", args)
		}
	}

	// Files: into a directory, to a name, and into the current directory.
	out := t.TempDir()
	if err := runQS("download", "vm1", "big.bin", out); err != nil {
		t.Fatal(err)
	}
	if err := runQS("download", "vm1", "a.txt", filepath.Join(out, "copy.txt")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(out)
	if err := runQS("download", "vm1", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	checkTree(t, out, map[string][]byte{"big.bin": big, "copy.txt": []byte("beta"), "renamed.txt": []byte("alpha")})
	if _, err := os.Stat(filepath.Join(out, "big.bin.qs-part")); err == nil {
		t.Error("a .qs-part file was left behind")
	}
	if err := runQS("download", "vm1", "missing", out); err == nil {
		t.Error("downloaded a missing file")
	}
}

func TestDownloadDirectories(t *testing.T) {
	_, guest := startServer(t)
	big := make([]byte, 3<<20)
	rand.Read(big)
	tree := map[string][]byte{"a.txt": []byte("alpha"), "empty": {}, "deep/er/big.bin": big}
	writeTree(t, filepath.Join(guest, "proj"), tree)
	os.MkdirAll(filepath.Join(guest, "proj", "emptydir"), 0o755)
	out := t.TempDir()

	if err := runQS("download", "vm1", "proj", out); err == nil || !strings.Contains(err.Error(), "-r") {
		t.Errorf("directory without -r: %v", err)
	}
	// -r: a zip.
	if err := runQS("download", "-r", "vm1", "proj", out); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(filepath.Join(out, "proj.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 6 { // 3 files, deep/, deep/er/, emptydir/
		t.Errorf("zip has %d entries", len(zr.File))
	}
	zr.Close()
	// -r -u: extracted into out/proj, or into a new directory by name.
	if err := runQS("download", "-r", "-u", "vm1", "proj", out); err != nil {
		t.Fatal(err)
	}
	checkTree(t, filepath.Join(out, "proj"), tree)
	if info, err := os.Stat(filepath.Join(out, "proj", "emptydir")); err != nil || !info.IsDir() {
		t.Errorf("empty directory not extracted: %v", err)
	}
	if err := runQS("download", "-r", "-u", "vm1", "proj", filepath.Join(out, "renamed")); err != nil {
		t.Fatal(err)
	}
	checkTree(t, filepath.Join(out, "renamed"), tree)

	// -e: an encrypted zip, of a file or (with -r) a directory.
	if err := runQS("download", "-e", "s3cret", "vm1", "proj/a.txt", out); err != nil {
		t.Fatal(err)
	}
	if err := runQS("download", "vm1", "proj", out, "-r", "-e", "s3cret"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt.zip", "proj.zip"} {
		zr, err := zip.OpenReader(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			if !f.Mode().IsDir() && f.Flags&1 == 0 {
				t.Errorf("%s: %s isn't encrypted", name, f.Name)
			}
		}
		zr.Close()
	}

	for _, args := range [][]string{
		{"-e", "", "vm1", "proj/a.txt", out},
		{"-u", "vm1", "proj", out},
		{"-r", "-u", "-e", "pw", "vm1", "proj", out},
	} {
		if err := runQS(append([]string{"download"}, args...)...); err == nil {
			t.Errorf("download %q succeeded", args)
		}
	}
}

func TestExtractZipRejectsEscapes(t *testing.T) {
	for _, name := range []string{"../evil", "/abs/evil", "a/../../evil"} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(name)
		w.Write([]byte("x"))
		zw.Close()
		dir := t.TempDir()
		if err := extractZip(&buf, filepath.Join(dir, "out"), nil); err == nil {
			t.Errorf("%q extracted", name)
		}
		if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
			t.Errorf("%q escaped", name)
		}
	}
}

func TestDir(t *testing.T) {
	_, guest := startServer(t)
	writeTree(t, guest, map[string][]byte{"a.txt": []byte("alpha"), "sub/b.txt": []byte("beta!")})
	var err error
	out, _ := capture(t, "", func() { err = runQS("dir", "vm1") })
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[0] != guest || !strings.Contains(out, "<DIR>  sub") || !strings.Contains(out, "5  a.txt") ||
		!strings.HasSuffix(out, "1 folder, 1 file, 5 bytes\n") {
		t.Errorf("listing:\n%s", out)
	}
	out, _ = capture(t, "", func() { err = runQS("dir", "vm1", "sub") })
	if err != nil || !strings.Contains(out, "b.txt") || strings.Contains(out, "a.txt") {
		t.Errorf("sub: %v\n%s", err, out)
	}
	if err := runQS("dir", "vm1", "missing"); err == nil {
		t.Error("listed a missing directory")
	}
}
