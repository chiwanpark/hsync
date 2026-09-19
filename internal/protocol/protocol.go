package protocol

type IndexResponse struct {
	Commit string            `json:"commit"`
	Root   string            `json:"root"`
	Files  map[string]string `json:"files"`
}

type PushRequest struct {
	Parent string            `json:"parent"`
	Files  map[string]string `json:"files"`
	Blobs  map[string]string `json:"blobs"`
}

type PushResponse struct {
	Commit string            `json:"commit"`
	Files  map[string]string `json:"files"`
}
