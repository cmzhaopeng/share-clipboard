package app

import (
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Extensions that Go's built-in table and many minimal servers don't know.
var extraMimeTypes = map[string]string{
	".heic": "image/heic",
	".heif": "image/heif",
	".md":   "text/markdown",
	".mov":  "video/quicktime",
	".m4a":  "audio/mp4",
	".mp3":  "audio/mpeg",
	".mp4":  "video/mp4",
	".webp": "image/webp",
	".csv":  "text/csv",
	".log":  "text/plain",
	".txt":  "text/plain",
	".yaml": "application/yaml",
	".yml":  "application/yaml",
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

func isGenericMimeType(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "", "application/octet-stream", "binary/octet-stream", "application/unknown", "application/x-unknown", "application/force-download":
		return true
	}
	return false
}

// detectMimeType keeps a specific type declared by the uploader, and otherwise
// derives one from the file name, then from the file's first bytes.
func detectMimeType(declared, fileName, path string) string {
	if !isGenericMimeType(declared) {
		return declared
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if byExt, ok := extraMimeTypes[ext]; ok {
		return byExt
	}
	if byExt := mime.TypeByExtension(ext); byExt != "" {
		return byExt
	}
	if path != "" {
		if f, err := os.Open(path); err == nil {
			head := make([]byte, 512)
			n, _ := io.ReadFull(f, head)
			_ = f.Close()
			if n > 0 {
				if sniffed := http.DetectContentType(head[:n]); !isGenericMimeType(sniffed) {
					return sniffed
				}
			}
		}
	}
	return "application/octet-stream"
}

// fixGenericMimeTypes re-detects attachments stored with a generic type by earlier versions.
func (a *App) fixGenericMimeTypes() error {
	rows, err := a.db.Query(`SELECT item_id, stored_name, name, mime_type FROM attachments`)
	if err != nil {
		return err
	}
	type fix struct{ itemID, storedName, mimeType string }
	var fixes []fix
	for rows.Next() {
		var itemID, storedName, name, mimeType string
		if err := rows.Scan(&itemID, &storedName, &name, &mimeType); err != nil {
			rows.Close()
			return err
		}
		if !isGenericMimeType(mimeType) {
			continue
		}
		detected := detectMimeType(mimeType, name, filepath.Join(a.cfg.DataDir, "files", itemID, storedName))
		if detected != mimeType {
			fixes = append(fixes, fix{itemID, storedName, detected})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, f := range fixes {
		if _, err := a.db.Exec(`UPDATE attachments SET mime_type = ? WHERE item_id = ? AND stored_name = ?`, f.mimeType, f.itemID, f.storedName); err != nil {
			return err
		}
	}
	if len(fixes) > 0 {
		log.Printf("re-detected content type for %d attachment(s)", len(fixes))
	}
	return nil
}
