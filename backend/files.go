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

var rootUploadPath = "/app/files/"

type FileResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	FileType string `json:"filetype"`
}

type FileMetadata struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Hash     string `json:"hash"`
	Type     string `json:"type"`
	Delete   bool   `json:"delete"`
}

var maxBytesReader *http.MaxBytesError

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

	// Fallback: read from YAML (legacy local files)
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

func serveFile(w http.ResponseWriter, r *http.Request) {
	fileId := chi.URLParam(r, "fileid")
	if len(fileId) < 4 {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	meta, err := dbGetFileMetadata(ctx, fileId)
	if err != nil || meta.Delete {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.QueryEscape(meta.Filename))

	if r2Enabled {
		key := r2ObjectKey(meta.Hash)
		if r2PublicURL != "" {
			// Redirect to public R2 URL
			http.Redirect(w, r, r2PublicURL+"/"+key, http.StatusFound)
			return
		}
		// Proxy through backend
		body, contentType, err := r2Download(ctx, key)
		if err != nil {
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
	filePath := filepath.Join(rootUploadPath, meta.Hash[:2], meta.Hash[2:4], meta.Hash)
	http.ServeFile(w, r, filePath)
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	cfg := getChannelConfig(ctx, slug)

	r.Body = http.MaxBytesReader(w, r.Body, int64(cfg.MaxFileSize)<<20)

	file, handler, err := r.FormFile("file")
	if err != nil {
		if errors.As(err, &maxBytesReader) {
			http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file into memory for hashing + type detection + upload
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	t, _ := filetype.Match(fileBytes[:min(512, len(fileBytes))])

	// Compute SHA-256 hash
	hashBytes := sha256.Sum256(fileBytes)
	fileHash := hex.EncodeToString(hashBytes[:])

	id := generatedRandomID(20)
	if id == "" {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	safeFilename := gozaru.Sanitize(handler.Filename)
	contentType := t.MIME.Value
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if r2Enabled {
		key := r2ObjectKey(fileHash)
		if !r2Exists(ctx, key) {
			if err := r2Upload(ctx, key, bytes.NewReader(fileBytes), contentType); err != nil {
				http.Error(w, "error uploading file", http.StatusInternalServerError)
				return
			}
		}
	} else {
		// Local storage
		if err := os.MkdirAll(rootUploadPath, os.ModePerm); err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		hashSubDir := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4])
		if err := os.MkdirAll(hashSubDir, os.ModePerm); err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		destPath := filepath.Join(hashSubDir, fileHash)
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			if err := os.WriteFile(destPath, fileBytes, 0644); err != nil {
				http.Error(w, "error", http.StatusInternalServerError)
				return
			}
		}
	}

	meta := &FileMetadata{
		ID:       id,
		Filename: safeFilename,
		Hash:     fileHash,
		Type:     t.MIME.Type,
		Delete:   false,
	}
	if err := dbSaveFileMetadata(ctx, meta); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	fileUrl := "/api/channel/" + slug + "/files/" + id

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FileResponse{
		URL:      fileUrl,
		Filename: handler.Filename,
		FileType: t.MIME.Type,
	})
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
	if err != nil || meta.Delete {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	if r2Enabled {
		key := r2ObjectKey(meta.Hash)
		if r2PublicURL != "" {
			http.Redirect(w, r, r2PublicURL+"/"+key, http.StatusFound)
			return
		}
		body, _, err := r2Download(ctx, key)
		if err != nil {
			http.ServeFile(w, r, "assets/favicon.ico")
			return
		}
		defer body.Close()
		io.Copy(w, body)
		return
	}

	filePath := filepath.Join(rootUploadPath, meta.Hash[:2], meta.Hash[2:4], meta.Hash)
	http.ServeFile(w, r, filePath)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
