package clientui

type ProcessOutputChunk struct {
	ProcessID   string
	OffsetBytes int64
	Text        string
}
