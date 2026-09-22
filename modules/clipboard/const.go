package clipboard

// Module identity and option keys.
//
// These live in one place so the config keys, the HTTP API and the history
// store can never drift apart.
const (
	// moduleID is the stable identifier used by config, hotkeys and the API.
	// It must equal the directory name.
	moduleID = "clipboard"

	// Option keys declared by Options().
	optMaxItems      = "max_items"
	optStoreImages   = "store_images"
	optPasteOnCopy   = "paste_on_copy"
	optRetentionDays = "retention_days"

	// Action ids declared by Actions().
	actionOpen  = "open_history"
	actionClear = "clear_history"
)

// Option defaults. They mirror internal/config.Default() so the panel and the
// runtime behaviour agree even before the user changes anything.
const (
	defaultMaxItems      = 500
	defaultStoreImages   = true
	defaultPasteOnCopy   = false
	defaultRetentionDays = 30
)
