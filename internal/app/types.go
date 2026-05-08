package app

import "time"

type Config struct {
	DataDir                string
	BootstrapAdminUsername string
	BootstrapAdminPassword string
	SessionSecret          string
	CookieSecure           bool
	StaticDir              string
	PublicBaseURL          string
	AllowedOrigins         []string
	AllowInsecureHTTP      bool
}

type Attachment struct {
	Name       string `json:"name"`
	StoredName string `json:"storedName"`
	Size       int64  `json:"size"`
	MimeType   string `json:"mimeType"`
	URL        string `json:"url"`
	PreviewURL string `json:"previewUrl,omitempty"`
}

type Item struct {
	ID          string       `json:"id"`
	Message     string       `json:"message"`
	CreatedAt   time.Time    `json:"createdAt"`
	CreatedBy   string       `json:"createdBy"`
	Visibility  string       `json:"visibility"`
	Attachments []Attachment `json:"attachments"`
}

type UserRecord struct {
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"isAdmin"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
