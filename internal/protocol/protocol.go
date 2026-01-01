package protocol

type SyncRequest struct {
	Filename string `json:"filename"`
	Base     string `json:"base"`
	Latest   string `json:"latest"`
}

type SyncResponse struct {
	Synced string `json:"synced"`
}