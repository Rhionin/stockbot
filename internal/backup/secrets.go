package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (r *Runner) archiveSecrets(ctx context.Context, now time.Time) error {
	if !r.IncludeSecrets {
		return nil
	}
	if len(r.SecretsPaths) == 0 {
		return nil
	}
	var tarBuf stringsWriter
	count, err := writeSecretsTar(&tarBuf, r.SecretsPaths)
	if err != nil {
		return err
	}
	var cipher stringsWriter
	if err := AgeEncrypt(bytesReader(tarBuf.b), r.Recipients, &cipher); err != nil {
		return err
	}
	sum := sha256.Sum256(cipher.b)
	dataKey, manifestKey := secretsObjectNames(now)
	if err := r.putVerified(ctx, dataKey, cipher.b, sum[:]); err != nil {
		return err
	}
	raw, err := json.Marshal(SecretsManifest{
		ObjectKey:        dataKey,
		CiphertextSHA256: hex.EncodeToString(sum[:]),
		CiphertextSize:   int64(len(cipher.b)),
		FileCount:        count,
		ToolVersion:      r.version(),
		CreatedAt:        now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return errWrap(ExitUpload, "secrets", "could not write the secrets manifest", err)
	}
	if err := r.putVerified(ctx, manifestKey, raw, nil); err != nil {
		return errWrap(ExitUpload, "secrets", "could not upload the secrets manifest", err)
	}
	return nil
}

type stringsWriter struct{ b []byte }

func (w *stringsWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

type byteReader struct {
	b []byte
	i int
}

func bytesReader(b []byte) io.Reader { return &byteReader{b: b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func writeSecretsTar(w io.Writer, paths []string) (int, error) {
	tw := tar.NewWriter(w)
	count := 0
	for _, path := range paths {
		n, err := addSecretPath(tw, path)
		if err != nil {
			_ = tw.Close()
			return count, err
		}
		count += n
	}
	if err := tw.Close(); err != nil {
		return count, errWrap(ExitUpload, "secrets", "could not finish the secrets archive", err)
	}
	if count == 0 {
		return 0, errClass(ExitPreflight, "secrets", "the secrets archive has no files")
	}
	return count, nil
}

func addSecretPath(tw *tar.Writer, path string) (int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, errClass(ExitPreflight, "secrets", "a secrets path does not exist")
		}
		return 0, errWrap(ExitPreflight, "secrets", "could not read a secrets path", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, errClass(ExitPreflight, "secrets", "a secrets path is a symlink")
	}
	if info.IsDir() {
		n := 0
		err := filepath.WalkDir(path, func(name string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errClass(ExitPreflight, "secrets", "a secrets path is a symlink")
			}
			if err := addSecretFile(tw, name); err != nil {
				return err
			}
			n++
			return nil
		})
		if err != nil {
			return n, err
		}
		return n, nil
	}
	if err := addSecretFile(tw, path); err != nil {
		return 0, err
	}
	return 1, nil
}

func addSecretFile(tw *tar.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return errWrap(ExitPreflight, "secrets", "could not read a secrets path", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return errWrap(ExitPreflight, "secrets", "could not read a secrets path", err)
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return errWrap(ExitUpload, "secrets", "could not archive a secrets path", err)
	}
	hdr.Name = tarName(path)
	if err := tw.WriteHeader(hdr); err != nil {
		return errWrap(ExitUpload, "secrets", "could not archive a secrets path", err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return errWrap(ExitUpload, "secrets", "could not archive a secrets path", err)
	}
	return nil
}

func tarName(path string) string {
	clean := filepath.Clean(path)
	clean = strings.TrimPrefix(clean, string(filepath.Separator))
	return clean
}
