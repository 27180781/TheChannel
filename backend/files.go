package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/go-chi/chi"
	"github.com/h2non/filetype"
	"github.com/icza/dyno"
	"github.com/subosito/gozaru"
	"gopkg.in/yaml.v3"
)

// compressWithTinyPng compresses an image using the TinyPNG API.
// Returns the compressed bytes, or the original bytes if compression fails or is not applicable.
func compressWithTinyPng(ctx context.Context, apiKey string, data []byte, mimeType string) []byte {
	// Only compress supported image types
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return data
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tinify.com/shrink", bytes.NewReader(data))
	if err != nil {
		return data
	}
	req.SetBasicAuth("api", apiKey)
	req.Header.Set("Content-Type", mimeType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return data
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return data
	}

	var result struct {
		Output struct {
			URL string `json:"url"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Output.URL == "" {
		return data
	}

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, result.Output.URL, nil)
	if err != nil {
		return data
	}
	dlReq.SetBasicAuth("api", apiKey)

	dlResp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return data
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return data
	}

	compressed, err := io.ReadAll(dlResp.Body)
	if err != nil || len(compressed) == 0 {
		return data
	}
	return compressed
}

var rootUploadPath = "/app/files/"

type FileResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	FileType string `json:"filetype"`
}

type FileMetadata struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Hash        string `json:"hash"`
	Type        string `json:"type"`
	Delete      bool   `json:"delete"`
	Size        int64  `json:"size"`        // bytes
	ChannelSlug string `json:"channelSlug"` // which channel owns this file
}

// allowLegacyUnscopedFiles re-opens the pre-migration corpus (records with no
// ChannelSlug) to every tenant. Off by default: unscoped records are the normal
// shape of every YAML-era file, so exempting them from isolation would leave a
// permanent cross-tenant read path.
var allowLegacyUnscopedFiles = os.Getenv("ALLOW_LEGACY_UNSCOPED_FILES") == "true"

// fileVisibleToChannel reports whether a file record may be served under slug.
func fileVisibleToChannel(meta *FileMetadata, slug string) bool {
	if meta.ChannelSlug == slug {
		return true
	}
	if meta.ChannelSlug == "" && allowLegacyUnscopedFiles {
		log.Printf("legacy unscoped file %s served to channel %s\n", meta.ID, slug)
		return true
	}
	return false
}

// dbSaveFileMetadata stores file metadata in Redis.
func dbSaveFileMetadata(ctx context.Context, meta *FileMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, "file:"+meta.ID, data, 0).Err()
}

// dbGetFileMetadata retrieves file metadata from Redis, falling back to YAML for old files.
func dbGetFileMetadata(ctx context.Context, id string) (*FileMetadata, error) {
	data, err := rdb.Get(ctx, "file:"+id).Result()
	if err == nil {
		var meta FileMetadata
		if err := json.Unmarshal([]byte(data), &meta); err != nil {
			return nil, err
		}
		return &meta, nil
	}

	// Fallback: read from YAML (legacy local files). The slicing below panics on
	// short ids, and callers feed this ids parsed out of Redis index members, so
	// malformed data must be an error rather than a crash.
	if len(id) < 4 {
		return nil, fmt.Errorf("file not found")
	}
	metadataFilePath := filepath.Join(rootUploadPath, id[:2], id[2:4], id+".yaml")
	yamlData, err := os.ReadFile(metadataFilePath)
	if err != nil {
		return nil, fmt.Errorf("file not found")
	}
	var raw map[string]any
	if err := yaml.Unmarshal(yamlData, &raw); err != nil {
		return nil, err
	}
	deleted, _ := raw["delete"].(bool)
	hash, _ := dyno.GetString(raw["hash"])
	filename, _ := dyno.GetString(raw["filename"])
	fileType, _ := dyno.GetString(raw["type"])
	return &FileMetadata{
		ID:       id,
		Filename: filename,
		Hash:     hash,
		Type:     fileType,
		Delete:   deleted,
	}, nil
}

// localBlobPath returns the on-disk path of a blob and whether the hash is
// well-formed enough to build one. Every caller slices hash[:2]/hash[2:4], so a
// short or empty hash (malformed metadata, a YAML-era record with no hash at
// all) would panic the handler; they go through here instead.
func localBlobPath(hash string) (string, bool) {
	if len(hash) < 4 {
		return "", false
	}
	return filepath.Join(rootUploadPath, hash[:2], hash[2:4], hash), true
}

// localBlobExists reports whether the local copy of a blob is present.
func localBlobExists(hash string) (string, bool) {
	p, ok := localBlobPath(hash)
	if !ok {
		return "", false
	}
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

func serveFile(w http.ResponseWriter, r *http.Request) {
	fileId := chi.URLParam(r, "fileid")
	if len(fileId) < 4 {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	meta, err := dbGetFileMetadata(ctx, fileId)
	if err != nil || meta.Delete {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	// Enforce channel isolation: reject if the file belongs to another channel.
	if !fileVisibleToChannel(meta, slug) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.QueryEscape(meta.Filename))

	if r2Enabled && len(meta.Hash) >= 4 {
		key := r2ObjectKey(meta.Hash)

		// Flipping R2 on does not move the historical corpus with it: the
		// local-to-R2 migration runs on the next boot and may not have reached
		// this blob (or may have failed for it). Redirecting to a key that is
		// not there yet would 404 every legacy image, so a miss falls back to
		// the local copy, which the migration never deletes.
		if !r2Exists(ctx, key) {
			if filePath, ok := localBlobExists(meta.Hash); ok {
				log.Printf("serveFile: %s/%s not in R2 (key %s), serving local copy\n", slug, fileId, key)
				http.ServeFile(w, r, filePath)
				return
			}
			log.Printf("serveFile: %s/%s missing in R2 (key %s) and on local disk\n", slug, fileId, key)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}

		if r2PublicURL != "" {
			// Public bucket: redirect to CDN URL (fastest, no auth needed)
			http.Redirect(w, r, r2PublicURL+"/"+key, http.StatusFound)
			return
		}
		// Private bucket: generate a pre-signed URL (1-hour TTL).
		// The client fetches directly from R2 — backend is not in the data path.
		presignedURL, err := r2PresignURL(ctx, key, time.Hour)
		if err == nil {
			http.Redirect(w, r, presignedURL, http.StatusFound)
			return
		}
		log.Printf("serveFile: presign failed for %s/%s (key %s): %v\n", slug, fileId, key, err)
		// Fallback: proxy through backend if presigning fails
		body, contentType, err := r2Download(ctx, key)
		if err != nil {
			log.Printf("serveFile: R2 download failed for %s/%s (key %s): %v\n", slug, fileId, key, err)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		defer body.Close()
		if contentType != nil {
			w.Header().Set("Content-Type", *contentType)
		}
		io.Copy(w, body)
		return
	}

	// Local storage fallback
	filePath, ok := localBlobPath(meta.Hash)
	if !ok {
		log.Printf("serveFile: %s/%s has malformed hash %q\n", slug, fileId, meta.Hash)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, filePath)
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	// Derived from the request so a client disconnect stops the work, and wide
	// enough to cover reading the body off the wire. The hostile sub-operations
	// (TinyPNG, the R2 write) get their own budgets below.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	slug := channelSlugFromCtx(r)
	cfg := getChannelConfig(ctx, slug)

	// Defensive clamp: getChannelConfig is not the only way a config can reach
	// here, and a non-positive or huge value would either reject every upload
	// or let a single request buffer unbounded bytes in memory.
	maxMB := cfg.MaxFileSize
	if maxMB <= 0 || maxMB > maxAllowedFileSizeMB {
		maxMB = defaultMaxFileSizeMB
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMB<<20)

	file, handler, err := r.FormFile("file")
	if err != nil {
		// The errors.As target must be a local: a package-level one is written
		// concurrently by every upload.
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			log.Printf("uploadFile: %s: rejected upload over the %d MB limit: %v\n", slug, maxMB, err)
			http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
			return
		}
		log.Printf("uploadFile: %s: reading multipart form failed: %v\n", slug, err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		log.Printf("uploadFile: %s: reading uploaded body %q failed: %v\n", slug, handler.Filename, err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	t, _ := filetype.Match(fileBytes[:min(512, len(fileBytes))])

	// Compress images with TinyPNG if the channel has an API key configured
	if cfg.TinyPngApiKey != "" {
		tinyCtx, tinyCancel := context.WithTimeout(ctx, 20*time.Second)
		fileBytes = compressWithTinyPng(tinyCtx, cfg.TinyPngApiKey, fileBytes, t.MIME.Value)
		tinyCancel()
	}

	fileSize := int64(len(fileBytes))
	hashBytes := sha256.Sum256(fileBytes)
	fileHash := hex.EncodeToString(hashBytes[:])

	// Quota check + auto-cleanup
	if err := enforceStorageQuota(ctx, slug, fileSize); err != nil {
		log.Printf("uploadFile: %s: quota check rejected %d bytes: %v\n", slug, fileSize, err)
		http.Error(w, err.Error(), http.StatusInsufficientStorage)
		return
	}

	id := generatedRandomID(20)
	if id == "" {
		log.Printf("uploadFile: %s: crypto/rand failed to generate a file id\n", slug)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	safeFilename := gozaru.Sanitize(handler.Filename)
	contentType := t.MIME.Value
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	isNewHash := true
	if r2Enabled {
		key := r2ObjectKey(fileHash)
		if r2Exists(ctx, key) {
			isNewHash = false
		} else {
			upCtx, upCancel := context.WithTimeout(ctx, 60*time.Second)
			err := r2Upload(upCtx, key, bytes.NewReader(fileBytes), contentType)
			upCancel()
			if err != nil {
				// The SDK error is printed verbatim, not summarised: its code is
				// the only thing that separates AccessDenied (a read-only or
				// wrong-bucket API token), NoSuchBucket, InvalidAccessKeyId and
				// SignatureDoesNotMatch — all of which surface here as the same
				// opaque 500 for the operator.
				log.Printf("uploadFile: %s: R2 upload of file %s failed (bucket %q, key %s, %d bytes, content-type %s): %v\n",
					slug, id, r2Bucket, key, fileSize, contentType, err)
				http.Error(w, "error uploading file", http.StatusInternalServerError)
				return
			}
		}
	} else {
		if err := os.MkdirAll(rootUploadPath, os.ModePerm); err != nil {
			log.Printf("uploadFile: %s: mkdir root upload path %s failed: %v\n", slug, rootUploadPath, err)
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		hashSubDir := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4])
		if err := os.MkdirAll(hashSubDir, os.ModePerm); err != nil {
			log.Printf("uploadFile: %s: mkdir %s for file %s failed: %v\n", slug, hashSubDir, id, err)
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		destPath := filepath.Join(hashSubDir, fileHash)
		if _, statErr := os.Stat(destPath); os.IsNotExist(statErr) {
			if err := os.WriteFile(destPath, fileBytes, 0644); err != nil {
				log.Printf("uploadFile: %s: writing %s for file %s (%d bytes) failed: %v\n", slug, destPath, id, fileSize, err)
				http.Error(w, "error", http.StatusInternalServerError)
				return
			}
		} else {
			isNewHash = false
		}
	}
	_ = isNewHash

	dbIncrFileHashRefs(ctx, fileHash)

	meta := &FileMetadata{
		ID:          id,
		Filename:    safeFilename,
		Hash:        fileHash,
		Type:        t.MIME.Type,
		Delete:      false,
		Size:        fileSize,
		ChannelSlug: slug,
	}
	if err := dbSaveFileMetadata(ctx, meta); err != nil {
		log.Printf("uploadFile: %s: saving metadata for file %s failed: %v\n", slug, id, err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	dbIncrChannelStorageUsed(ctx, slug, fileSize)
	dbAddChannelFile(ctx, slug, id, time.Now().Unix(), fileSize)

	fileUrl := "/api/channel/" + slug + "/files/" + id

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FileResponse{
		URL:      fileUrl,
		Filename: handler.Filename,
		FileType: t.MIME.Type,
	})
}

// enforceStorageQuota checks quota and runs auto-cleanup if needed.
// Returns error if quota is exceeded and auto-cleanup is disabled or insufficient.
func enforceStorageQuota(ctx context.Context, slug string, newFileSize int64) error {
	// All three config reads fail closed: dbGetEffectiveStorageQuota and
	// dbGetChannelStorageUsed already map redis.Nil to defaults, so only real
	// failures surface here — and a real failure silently disabling quota
	// enforcement would let usage grow past quota unnoticed.
	quota, err := dbGetEffectiveStorageQuota(ctx, slug)
	if err != nil {
		// The client only ever sees "storage configuration unavailable", which
		// is indistinguishable from a genuine quota breach; the real cause (a
		// failed Redis read) only exists here.
		log.Printf("enforceStorageQuota: %s: reading effective quota failed: %v\n", slug, err)
		return fmt.Errorf("storage configuration unavailable")
	}
	if quota == 0 {
		return nil
	}

	used, err := dbGetChannelStorageUsed(ctx, slug)
	if err != nil {
		log.Printf("enforceStorageQuota: %s: reading used bytes failed: %v\n", slug, err)
		return fmt.Errorf("storage configuration unavailable")
	}

	if used+newFileSize <= quota {
		return nil // within quota
	}

	autoCleanup, err := dbGetChannelAutoCleanup(ctx, slug)
	if err != nil {
		log.Printf("enforceStorageQuota: %s: reading auto-cleanup flag failed: %v\n", slug, err)
		return fmt.Errorf("storage configuration unavailable")
	}
	if !autoCleanup {
		return fmt.Errorf("storage quota exceeded (%d/%d bytes)", used, quota)
	}

	// Auto-cleanup: delete oldest files until we have enough space (target: 80% of quota)
	target := int64(float64(quota) * 0.80)
	needToFree := (used + newFileSize) - target

	files, err := dbGetOldestChannelFiles(ctx, slug, 200)
	if err != nil {
		log.Printf("enforceStorageQuota: %s: listing oldest files for auto-cleanup failed: %v\n", slug, err)
		return fmt.Errorf("storage quota exceeded")
	}

	for _, f := range files {
		if needToFree <= 0 {
			break
		}
		deleteFileByID(ctx, slug, f.ID, true)
		needToFree -= f.Size
	}

	// Cleanup is best-effort: it walks at most the 200 oldest files, so it can
	// finish without having made room. Reporting success here would let the
	// channel grow past its quota indefinitely.
	if needToFree > 0 {
		return fmt.Errorf("storage quota exceeded (%d/%d bytes); auto-cleanup could not free enough space", used, quota)
	}

	return nil
}

// deleteFileByID marks a file as deleted, decrements storage counter, and removes
// from R2/disk if no more refs. removeFromIndex controls whether the channel's
// file tracking set is updated; dbDeleteChannel passes false because it drops
// the whole set itself (and a per-file O(N) scan would make deletion O(N^2)).
func deleteFileByID(ctx context.Context, slug, fileID string, removeFromIndex bool) {
	meta, err := dbGetFileMetadata(ctx, fileID)
	if err != nil || meta.Delete {
		return
	}

	// Claim the deletion atomically: two concurrent callers (e.g. parallel
	// near-quota uploads running auto-cleanup) would otherwise both pass the
	// meta.Delete check and double-decrement used_bytes and the hash refcount,
	// deleting a blob another channel still references.
	claimed, err := rdb.SetNX(ctx, "file:"+fileID+":deleting", 1, time.Minute).Result()
	if err != nil || !claimed {
		return
	}

	meta.Delete = true
	dbSaveFileMetadata(ctx, meta)
	dbDecrChannelStorageUsed(ctx, slug, meta.Size)
	if removeFromIndex {
		dbRemoveChannelFile(ctx, slug, fileID)
	}

	// Decrement hash refs and delete from storage if no more references
	refs, err := dbDecrFileHashRefs(ctx, meta.Hash)
	if err == nil && refs <= 0 {
		if r2Enabled && len(meta.Hash) >= 4 {
			if err := r2Delete(ctx, r2ObjectKey(meta.Hash)); err != nil {
				log.Printf("deleteFileByID: %s: R2 delete of %s (key %s) failed: %v\n", slug, fileID, r2ObjectKey(meta.Hash), err)
			}
		}
		// The local copy is removed either way: with R2 on it is the migration's
		// fallback copy of the same blob, and the last reference is gone.
		if p, ok := localBlobPath(meta.Hash); ok {
			os.Remove(p)
		}
		// Drop the counter itself; a fresh upload of the same hash starts at 1 again.
		dbDelFileHashRefs(ctx, meta.Hash)
	}
}

func generatedFileHash(file io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func generatedRandomID(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func getFavicon(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := r.URL.Query().Get("slug")
	if slug == "" {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	c, err := getChannelDetails(ctx, slug)
	if err != nil {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	logoUrl := c["logoUrl"]
	if logoUrl == "" {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	fileId := path.Base(logoUrl)
	if len(fileId) < 4 {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	meta, err := dbGetFileMetadata(ctx, fileId)
	// The logo URL is arbitrary moderator-supplied input, so the same channel
	// isolation serveFile applies must hold here too.
	if err != nil || meta.Delete || !fileVisibleToChannel(meta, slug) {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	if r2Enabled && len(meta.Hash) >= 4 {
		key := r2ObjectKey(meta.Hash)
		// Same partial-migration fallback as serveFile: a logo whose blob has
		// not been copied to R2 yet is still on local disk.
		if !r2Exists(ctx, key) {
			if filePath, ok := localBlobExists(meta.Hash); ok {
				log.Printf("getFavicon: %s: logo %s not in R2 (key %s), serving local copy\n", slug, fileId, key)
				http.ServeFile(w, r, filePath)
				return
			}
			log.Printf("getFavicon: %s: logo %s missing in R2 (key %s) and on local disk\n", slug, fileId, key)
			http.ServeFile(w, r, "assets/favicon.ico")
			return
		}
		if r2PublicURL != "" {
			http.Redirect(w, r, r2PublicURL+"/"+key, http.StatusFound)
			return
		}
		body, _, err := r2Download(ctx, key)
		if err != nil {
			log.Printf("getFavicon: %s: R2 download of logo %s (key %s) failed: %v\n", slug, fileId, key, err)
			http.ServeFile(w, r, "assets/favicon.ico")
			return
		}
		defer body.Close()
		io.Copy(w, body)
		return
	}

	filePath, ok := localBlobPath(meta.Hash)
	if !ok {
		log.Printf("getFavicon: %s: logo %s has malformed hash %q\n", slug, fileId, meta.Hash)
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}
	http.ServeFile(w, r, filePath)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
