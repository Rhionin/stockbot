package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Store uploads to an S3-compatible endpoint using static credentials.
// Credentials come from the caller. The AWS default environment chain is
// not used.
type S3Store struct {
	client *s3.Client
	bucket string
	clock  *responseClock
}

// responseClock records the Date header from R2. That is the server time
// the stale check uses, not the Pi clock.
type responseClock struct {
	base http.RoundTripper
	mu   sync.Mutex
	at   time.Time
}

func (c *responseClock) RoundTrip(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if resp != nil {
		if server, perr := http.ParseTime(resp.Header.Get("Date")); perr == nil {
			c.mu.Lock()
			c.at = server.UTC()
			c.mu.Unlock()
		}
	}
	return resp, err
}

func (c *responseClock) time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// NewS3Store builds a path-style client. endpoint is the R2 or test URL.
func NewS3Store(endpoint, bucket, accessKey, secret string, httpClient *http.Client) (*S3Store, error) {
	if accessKey == "" || secret == "" {
		return nil, errClass(ExitPreflight, "preflight", "the R2 access key is missing from the settings file")
	}
	if bucket == "" {
		return nil, errClass(ExitPreflight, "preflight", "the backup bucket name is missing")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	// Copy the client so the caller's transport, including http.DefaultClient,
	// is not replaced.
	cloned := *httpClient
	clock := &responseClock{base: cloned.Transport}
	cloned.Transport = clock
	client := s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secret, ""),
		HTTPClient:   &cloned,
		// Only checksums this client sets explicitly. The default chain would
		// also read environment credentials; this provider does not.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &S3Store{client: client, bucket: bucket, clock: clock}, nil
}

// ServerNow is the Date header from the latest R2 response. A list or head
// in this process records it. The Pi clock is not consulted.
func (s *S3Store) ServerNow(ctx context.Context) (time.Time, error) {
	if s.clock != nil {
		if server := s.clock.time(); !server.IsZero() {
			return server, nil
		}
	}
	if _, err := s.List(ctx, "nightly/"); err != nil {
		return time.Time{}, err
	}
	if s.clock != nil {
		if server := s.clock.time(); !server.IsZero() {
			return server, nil
		}
	}
	return time.Time{}, errClass(ExitSweep, "clock", "the backup server did not report a time")
}

func (s *S3Store) Put(ctx context.Context, up Upload) error {
	body, sum, size, err := readUpload(up)
	if err != nil {
		return errWrap(ExitUpload, "upload", "could not read the backup", err)
	}
	checksum := base64.StdEncoding.EncodeToString(sum)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(s.bucket),
		Key:               aws.String(up.Key),
		Body:              bytes.NewReader(body),
		ContentLength:     aws.Int64(size),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
		ChecksumSHA256:    aws.String(checksum),
	})
	if err != nil {
		return errWrap(ExitUpload, "upload", "could not upload the backup", err)
	}
	return nil
}

func (s *S3Store) Head(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(key),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return ObjectInfo{}, errWrap(ExitUpload, "upload", "could not verify the uploaded backup", err)
	}
	info := ObjectInfo{
		Key:  key,
		Size: aws.ToInt64(out.ContentLength),
	}
	if out.LastModified != nil {
		info.LastModified = out.LastModified.UTC()
	}
	raw, err := base64.StdEncoding.DecodeString(aws.ToString(out.ChecksumSHA256))
	if err != nil || len(raw) != sha256.Size {
		return ObjectInfo{}, errClass(ExitUpload, "upload", "the uploaded backup has no sha256 checksum")
	}
	info.SHA256 = raw
	return info, nil
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, errWrap(ExitUpload, "upload", "could not download the backup", err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, errWrap(ExitUpload, "upload", "could not download the backup", err)
	}
	return body, nil
}

func (s *S3Store) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	var token *string
	for {
		page, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, errWrap(ExitSweep, "sweep", "could not list backups", err)
		}
		for _, obj := range page.Contents {
			info := ObjectInfo{
				Key:  aws.ToString(obj.Key),
				Size: aws.ToInt64(obj.Size),
			}
			if obj.LastModified != nil {
				info.LastModified = obj.LastModified.UTC()
			}
			out = append(out, info)
		}
		if page.IsTruncated == nil || !*page.IsTruncated || page.NextContinuationToken == nil {
			break
		}
		token = page.NextContinuationToken
	}
	return out, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return errWrap(ExitSweep, "sweep", "could not delete an expired backup", err)
	}
	return nil
}

func readUpload(up Upload) ([]byte, []byte, int64, error) {
	body, err := io.ReadAll(up.Body)
	if err != nil {
		return nil, nil, 0, err
	}
	sum := sha256.Sum256(body)
	if up.SHA256 != nil && !bytes.Equal(up.SHA256, sum[:]) {
		return nil, nil, 0, errors.New("the backup checksum does not match its bytes")
	}
	if up.Size > 0 && up.Size != int64(len(body)) {
		return nil, nil, 0, errors.New("the backup size does not match its bytes")
	}
	return body, sum[:], int64(len(body)), nil
}

func sameSHA(a, b []byte) bool {
	return bytes.Equal(a, b)
}
