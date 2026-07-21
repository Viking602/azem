package session

// Attachment is a durable reference to a user-provided file (usually an image)
// stored under the application attachment directory.
type Attachment struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	MIME string `json:"mime,omitempty"`
	Path string `json:"path,omitempty"`
	Size int64  `json:"size,omitempty"`
}
