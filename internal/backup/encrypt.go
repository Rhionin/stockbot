package backup

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
)

// EncryptFile gzip-compresses path at level 6 and age-encrypts it to dst.
// The recipient text is public keys, one per line. Ciphertext is written
// only to dst.
func EncryptFile(path, recipientText string, dst io.Writer) error {
	recips, err := age.ParseRecipients(stringsReader(recipientText))
	if err != nil {
		return errWrap(ExitPreflight, "preflight", "could not read the age recipient", err)
	}
	if len(recips) == 0 {
		return errClass(ExitPreflight, "preflight", "no age recipient is configured")
	}
	in, err := os.Open(path)
	if err != nil {
		return errWrap(ExitPreflight, "preflight", "could not read the snapshot", err)
	}
	defer in.Close()
	return encryptReader(in, recips, dst)
}

// EncryptReader gzip-compresses r and age-encrypts it to dst.
func EncryptReader(r io.Reader, recipientText string, dst io.Writer) error {
	recips, err := age.ParseRecipients(stringsReader(recipientText))
	if err != nil {
		return errWrap(ExitPreflight, "preflight", "could not read the age recipient", err)
	}
	return encryptReader(r, recips, dst)
}

// AgeEncrypt age-encrypts r without compressing it. The secrets archive is
// already a tar; it is not gzipped a second time.
func AgeEncrypt(r io.Reader, recipientText string, dst io.Writer) error {
	recips, err := age.ParseRecipients(stringsReader(recipientText))
	if err != nil {
		return errWrap(ExitPreflight, "preflight", "could not read the age recipient", err)
	}
	if len(recips) == 0 {
		return errClass(ExitPreflight, "preflight", "no age recipient is configured")
	}
	enc, err := age.Encrypt(dst, recips...)
	if err != nil {
		return errWrap(ExitUpload, "upload", "could not encrypt the secrets archive", err)
	}
	if _, err := io.Copy(enc, r); err != nil {
		_ = enc.Close()
		return errWrap(ExitUpload, "upload", "could not encrypt the secrets archive", err)
	}
	if err := enc.Close(); err != nil {
		return errWrap(ExitUpload, "upload", "could not encrypt the secrets archive", err)
	}
	return nil
}

func encryptReader(r io.Reader, recips []age.Recipient, dst io.Writer) error {
	enc, err := age.Encrypt(dst, recips...)
	if err != nil {
		return errWrap(ExitUpload, "upload", "could not encrypt the backup", err)
	}
	gz, err := gzip.NewWriterLevel(enc, 6)
	if err != nil {
		return errWrap(ExitUpload, "upload", "could not compress the backup", err)
	}
	if _, err := io.Copy(gz, r); err != nil {
		_ = gz.Close()
		_ = enc.Close()
		return errWrap(ExitUpload, "upload", "could not compress the backup", err)
	}
	if err := gz.Close(); err != nil {
		_ = enc.Close()
		return errWrap(ExitUpload, "upload", "could not compress the backup", err)
	}
	if err := enc.Close(); err != nil {
		return errWrap(ExitUpload, "upload", "could not encrypt the backup", err)
	}
	return nil
}

// DecryptGunzip age-decrypts src with an identity file and gunzips it to dst.
// The backup tool's restore drill uses the age CLI; tests use this to prove
// the ciphertext round-trips.
func DecryptGunzip(src io.Reader, identity string, dst io.Writer) error {
	ids, err := age.ParseIdentities(stringsReader(identity))
	if err != nil {
		return fmt.Errorf("could not read the age identity: %s", err.Error())
	}
	plain, err := age.Decrypt(src, ids...)
	if err != nil {
		return fmt.Errorf("could not decrypt the backup: %s", err.Error())
	}
	gz, err := gzip.NewReader(plain)
	if err != nil {
		return fmt.Errorf("could not decompress the backup: %s", err.Error())
	}
	defer gz.Close()
	if _, err := io.Copy(dst, gz); err != nil {
		return fmt.Errorf("could not decompress the backup: %s", err.Error())
	}
	return nil
}

type stringReader struct {
	s string
	i int
}

func stringsReader(s string) io.Reader { return &stringReader{s: s} }

func (r *stringReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
