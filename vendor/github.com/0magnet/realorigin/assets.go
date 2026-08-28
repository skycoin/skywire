package realorigin

import _ "embed"

// The three files are the substrate's JavaScript half. They are embedded rather
// than served from disk so that a consumer is one import with nothing to deploy
// alongside it.

//go:embed web/bootstrap.html
var bootstrapHTML []byte

//go:embed web/sw.js
var swJS []byte

//go:embed web/responder.js
var responderJS []byte
