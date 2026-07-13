package routes

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZipSafe_RejectsZipSlip(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "bad.zip")
	dest := filepath.Join(tmp, "out")
	_ = os.MkdirAll(dest, 0o755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("pwned"))
	_ = zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	err = extractZipSafe(zipPath, dest, 50*1024*1024, 500)
	if err == nil {
		t.Fatal("expected zip-slip rejection")
	}
}

func TestExtractZipSafe_OK(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "ok.zip")
	dest := filepath.Join(tmp, "out")
	_ = os.MkdirAll(dest, 0o755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("<html>hi</html>"))
	_ = zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractZipSafe(zipPath, dest, 50*1024*1024, 500); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("hi")) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestExtractZipSafe_ActualSizeCap(t *testing.T) {
	// Zip-bomb style: two small-declared files that expand past max when counted by actual writes.
	// Standard archive/zip records real UncompressedSize64 from the writer, so we test the
	// remaining-budget LimitReader path by setting a tiny maxSize.
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "big.zip")
	dest := filepath.Join(tmp, "out")
	_ = os.MkdirAll(dest, 0o755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 1000)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	w2, err := zw.Create("b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// maxSize 1500: first file 1000 ok, second pushes total over
	err = extractZipSafe(zipPath, dest, 1500, 500)
	if err == nil {
		t.Fatal("expected total size rejection")
	}
}

func TestExtractZipSafe_TooManyFiles(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "many.zip")
	dest := filepath.Join(tmp, "out")
	_ = os.MkdirAll(dest, 0o755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 10; i++ {
		w, err := zw.Create(filepath.ToSlash(filepath.Join("f", string(rune('a'+i))+".txt")))
		if err != nil {
			// use simple names
			w, err = zw.Create(string(rune('a'+i)) + ".txt")
			if err != nil {
				t.Fatal(err)
			}
		}
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractZipSafe(zipPath, dest, 50*1024*1024, 5); err == nil {
		t.Fatal("expected too many files")
	}
}
