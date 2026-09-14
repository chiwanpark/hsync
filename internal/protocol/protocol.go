package protocol

type SyncRequest struct {
	Filename string `json:"filename"`
	Base     string `json:"base"`
	Latest   string `json:"latest"`
}

type SyncResponse struct {
	Synced string `json:"synced"`
}

type RenameRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
	Base string `json:"base"`
}

type Tombstone struct {
	DeletedAt int64  `json:"deletedAt"`
	RenamedTo string `json:"renamedTo,omitempty"`
}
