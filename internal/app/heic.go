package app

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// iPhones save photos and copied images as HEIC, which most browsers cannot display and the
// clipboard API cannot carry. When libheif's command-line tool is installed
// (apt install libheif-examples), HEIC uploads are converted to JPEG; otherwise they are kept as-is.

func isHEIC(mimeType, fileName string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "image/heic", "image/heif", "image/heic-sequence", "image/heif-sequence":
		return true
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	return ext == ".heic" || ext == ".heif"
}

func heifConverter() string {
	for _, name := range []string{"heif-dec", "heif-convert"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// convertHEICToJPEG writes a JPEG next to src and returns its path. The caller owns both files.
func convertHEICToJPEG(src string) (string, bool) {
	bin := heifConverter()
	if bin == "" {
		return "", false
	}
	dst := strings.TrimSuffix(src, filepath.Ext(src)) + "-" + randomID()[:8] + ".jpg"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-q", "90", src, dst).CombinedOutput()
	if err != nil {
		log.Printf("heic conversion failed for %s: %v: %s", filepath.Base(src), err, strings.TrimSpace(string(out)))
		_ = os.Remove(dst)
		return "", false
	}
	if _, err := os.Stat(dst); err != nil {
		// Files with several top-level images are written as <name>-1.jpg, <name>-2.jpg …; keep the first.
		matches, _ := filepath.Glob(strings.TrimSuffix(dst, ".jpg") + "-*.jpg")
		if len(matches) == 0 {
			return "", false
		}
		if err := os.Rename(matches[0], dst); err != nil {
			return "", false
		}
		for _, extra := range matches[1:] {
			_ = os.Remove(extra)
		}
	}
	return dst, true
}

func jpegName(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
}

// convertStoredHEIC converts existing HEIC attachments once, e.g. after the converter is installed.
func (a *App) convertStoredHEIC() error {
	if heifConverter() == "" {
		return nil
	}
	rows, err := a.db.Query(`SELECT item_id, stored_name, name, mime_type FROM attachments`)
	if err != nil {
		return err
	}
	type pending struct{ itemID, storedName, name string }
	var todo []pending
	for rows.Next() {
		var p pending
		var mimeType string
		if err := rows.Scan(&p.itemID, &p.storedName, &p.name, &mimeType); err != nil {
			rows.Close()
			return err
		}
		if isHEIC(mimeType, p.name) {
			todo = append(todo, p)
		}
	}
	rows.Close()
	for _, p := range todo {
		src := filepath.Join(a.cfg.DataDir, "files", p.itemID, p.storedName)
		dst, ok := convertHEICToJPEG(src)
		if !ok {
			continue
		}
		info, err := os.Stat(dst)
		if err != nil {
			continue
		}
		if _, err := a.db.Exec(`UPDATE attachments SET stored_name = ?, name = ?, size = ?, mime_type = 'image/jpeg' WHERE item_id = ? AND stored_name = ?`,
			filepath.Base(dst), jpegName(p.name), info.Size(), p.itemID, p.storedName); err != nil {
			_ = os.Remove(dst)
			return err
		}
		_ = os.Remove(src)
		log.Printf("converted stored HEIC attachment to JPEG: %s", filepath.Base(dst))
	}
	return nil
}
