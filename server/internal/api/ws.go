package api

import (
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// WSHandler handles the WebSocket upgrade endpoint GET /api/v1/ws.
// Authentication uses a JWT passed as the `token` query parameter instead of
// the Authorization header — browsers cannot set custom headers on WebSocket
// connections opened via the native WebSocket API.
//
// Topic subscription is declared at connection time via the `topics` query
// parameter. The notifications:<user_id> topic is always added automatically
// from the JWT claims so the client does not need to know its own user ID.
//
// Example connection URL:
//
//	ws://host/api/v1/ws?token=<jwt>&topics=job:uuid1,agent:uuid2
type WSHandler struct {
	hub    *websocket.Hub
	jwtMgr *auth.JWTManager
	logger *zap.Logger
}

// NewWSHandler creates a new WSHandler.
func NewWSHandler(hub *websocket.Hub, jwtMgr *auth.JWTManager, logger *zap.Logger) *WSHandler {
	return &WSHandler{
		hub:    hub,
		jwtMgr: jwtMgr,
		logger: logger.Named("ws_handler"),
	}
}

// ServeWS handles GET /api/v1/ws.
// It authenticates the request, builds the topic list, upgrades the connection,
// and starts the client read/write pumps. The handler blocks until the
// connection closes — this is expected for WebSocket handlers.
func (h *WSHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	// --- Authentication ---
	// JWT is passed as a query parameter because the browser WebSocket API
	// does not support custom headers. The token has the same short TTL
	// (15 min) as Bearer tokens — clients must reconnect with a fresh token
	// after expiry.
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		ErrUnauthorized(w)
		return
	}

	claims, err := h.jwtMgr.ValidateAccessToken(tokenStr)
	if err != nil {
		ErrUnauthorized(w)
		return
	}

	// --- Topic resolution ---
	topics := h.resolveTopics(r, claims)

	// --- Upgrade & run ---
	client, err := websocket.NewClient(h.hub, w, r, topics, h.logger)
	if err != nil {
		// Upgrade failure is already logged by gorilla; no need to log again.
		// The response has already been written by the upgrader on error.
		h.logger.Warn("ws: upgrade failed",
			zap.String("user_id", claims.UserID),
			zap.Error(err),
		)
		return
	}

	h.logger.Info("ws: client connected",
		zap.String("user_id", claims.UserID),
		zap.String("remote_addr", r.RemoteAddr),
		zap.Strings("topics", topics),
	)

	// Run blocks until the connection closes. readPump and writePump handle
	// cleanup and hub unregistration internally.
	client.Run()

	h.logger.Info("ws: client disconnected",
		zap.String("user_id", claims.UserID),
		zap.String("remote_addr", r.RemoteAddr),
	)
}

// resolveTopics builds the final topic list for a client connection.
// It combines:
//  1. Explicit topics from the `topics` query parameter (comma-separated).
//  2. The notifications:<user_id> topic, always added automatically from JWT.
//
// Unknown or malformed topic strings are silently ignored — the client will
// simply never receive messages for topics that do not exist.
// Admin users may subscribe to any topic; regular users are limited to the
// same set for now (ownership enforcement is handled at the publish site).
func (h *WSHandler) resolveTopics(r *http.Request, claims *auth.Claims) []string {
	seen := make(map[string]struct{})
	var topics []string

	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		if _, exists := seen[t]; !exists {
			seen[t] = struct{}{}
			topics = append(topics, t)
		}
	}

	// Always subscribe to the user's own notification channel.
	add("notifications:" + claims.UserID)

	// Optional explicit topics from query parameter.
	if raw := r.URL.Query().Get("topics"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			add(t)
		}
	}

	return topics
}