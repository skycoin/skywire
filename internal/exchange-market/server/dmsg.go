// Package server internal/server/dmsg.go
package server

// TODO: Implement dmsg server for handling client connections.
//
// This will include:
// - Listening for incoming dmsg connections from exchange-clients
// - Session management (single-session enforcement per public key)
// - Message routing and protocol handling
// - Request/response handling for all API endpoints defined in exchange-design.md
//
// The server will use the app.Client's dmsg capabilities provided by Skywire
// to establish secure, end-to-end encrypted connections with clients.
//
// See exchange-design.md Section 8 for the complete API specification.
