package event

// Limits bounds how much of a commit goes into one event. The byte limits
// apply to patch text before JSON encoding (escaping makes the JSON larger);
// they are not a limit on the size of the JSON document.
type Limits struct {
	MaxFiles           int // entries in the files array
	MaxFilePatchBytes  int // patch text per file
	MaxTotalPatchBytes int // patch text of all files together
}

// DefaultLimits is the single place where the size limits are defined.
var DefaultLimits = Limits{
	MaxFiles:           100,
	MaxFilePatchBytes:  64 << 10,  // 64 KiB
	MaxTotalPatchBytes: 512 << 10, // 512 KiB
}
