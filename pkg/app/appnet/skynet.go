// Package appnet pkg/app/appnet/skynet.go
package appnet

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AppType is the type of the network of the external app that is being published or connected to
type AppType string

const (
	// TCP is the type of network of the external app that is being published or connected to
	TCP AppType = "TCP"
	// UDP is the type of network of the external app that is being published or connected to
	UDP AppType = "UDP"
	// HTTP is the type of network of the external app that is being published or connected to
	HTTP AppType = "HTTP"
)

// NetManager manages all the connections and listeners
type NetManager struct {
	listeners map[uuid.UUID]*PublishLis
	conns     map[uuid.UUID]*ConnectConn
	mu        sync.Mutex
}

// NewNetManager creates a new NetManager
func NewNetManager() *NetManager {
	return &NetManager{
		listeners: make(map[uuid.UUID]*PublishLis),
		conns:     make(map[uuid.UUID]*ConnectConn),
	}
}

func (nm *NetManager) isPublishPortAvailable(addr Addr, localPort int, appType AppType) error {

	for _, l := range nm.listeners {
		if l.SkyAddr.GetPort() == addr.GetPort() {
			return fmt.Errorf("skyport %d is already in use for app type %v", addr.GetPort(), appType)
		}
		if l.LocalPort == localPort {
			return fmt.Errorf("local port %d is already in use for app type %v", localPort, appType)
		}
	}
	return nil
}

// IsPublishPortAvailable checks if a port and apptype is available for publishing
func (nm *NetManager) IsPublishPortAvailable(addr Addr, localPort int, appType AppType) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	return nm.isPublishPortAvailable(addr, localPort, appType)
}

// AddPublish adds publishListener to the NetManager
func (nm *NetManager) AddPublish(lis *PublishLis) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if err := nm.isPublishPortAvailable(lis.SkyAddr, lis.LocalPort, lis.AppType); err != nil {
		return err
	}

	nm.listeners[lis.ID] = lis
	return nil
}

// GetPublishListener get's a publishListener by ID
func (nm *NetManager) GetPublishListener(id uuid.UUID) *PublishLis {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	return nm.listeners[id]
}

// GetAllPublishListeners gets all publishListeners
func (nm *NetManager) GetAllPublishListeners() map[uuid.UUID]*PublishLis {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	return nm.listeners
}

// RemovePublishListener removes a publishListener by ID
func (nm *NetManager) RemovePublishListener(id uuid.UUID) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	delete(nm.listeners, id)
}

func (nm *NetManager) isConnectPortAvailable(webPort int) error {

	for _, c := range nm.conns {
		if c.WebPort == webPort {
			return fmt.Errorf("web port %d is already in use", webPort)
		}
	}
	return nil
}

// IsConnectPortAvailable checks if a web port is available
func (nm *NetManager) IsConnectPortAvailable(webPort int) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	return nm.isConnectPortAvailable(webPort)
}

// AddConnect adds ConnectConn to the NetManager
func (nm *NetManager) AddConnect(conn *ConnectConn) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if err := nm.isConnectPortAvailable(conn.WebPort); err != nil {
		return err
	}

	nm.conns[conn.ID] = conn
	return nil
}

// GetConnectConn get's a ConnectConn by ID
func (nm *NetManager) GetConnectConn(id uuid.UUID) *ConnectConn {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	return nm.conns[id]
}

// GetAllConnectConns gets all ConnectConns
func (nm *NetManager) GetAllConnectConns() map[uuid.UUID]*ConnectConn {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	return nm.conns
}

// RemoveConnectConn removes a ConnectConn by ID
func (nm *NetManager) RemoveConnectConn(id uuid.UUID) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	delete(nm.conns, id)
}

// Close closes all the connections and listeners
func (nm *NetManager) Close() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	for _, conn := range nm.conns {
		err := conn.Close()
		if err != nil {
			return err
		}
	}

	for _, lis := range nm.listeners {
		err := lis.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		if latency > time.Minute {
			latency = latency.Truncate(time.Second)
		}
		statusCode := c.Writer.Status()
		method := c.Request.Method
		path := c.Request.URL.Path
		// Get the background color based on the status code
		statusCodeBackgroundColor := getBackgroundColor(statusCode)
		// Get the method color
		methodColor := getMethodColor(method)
		// Print the logging in a custom format which includes the publickeyfrom c.Request.RemoteAddr ex.:
		// [SKYNET] 2023/05/18 - 19:43:15 | 200 |    10.80885ms |                 | 02b5ee5333aa6b7f5fc623b7d5f35f505cb7f974e98a70751cf41962f84c8c4637:49153 | GET      /node-info.json
		fmt.Printf("[SKYNET] %s |%s %3d %s| %13v | %15s | %72s |%s %-7s %s %s\n",
			time.Now().Format("2006/01/02 - 15:04:05"),
			statusCodeBackgroundColor,
			statusCode,
			resetColor(),
			latency,
			c.ClientIP(),
			c.Request.RemoteAddr,
			methodColor,
			method,
			resetColor(),
			path,
		)
	}
}

func getBackgroundColor(statusCode int) string {
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return green
	case statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest:
		return white
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return yellow
	default:
		return red
	}
}

func getMethodColor(method string) string {
	switch method {
	case http.MethodGet:
		return blue
	case http.MethodPost:
		return cyan
	case http.MethodPut:
		return yellow
	case http.MethodDelete:
		return red
	case http.MethodPatch:
		return green
	case http.MethodHead:
		return magenta
	case http.MethodOptions:
		return white
	default:
		return reset
	}
}

func resetColor() string {
	return reset
}

const (
	green   = "\033[97;42m"
	white   = "\033[90;47m"
	yellow  = "\033[90;43m"
	red     = "\033[97;41m"
	blue    = "\033[97;44m"
	magenta = "\033[97;45m"
	cyan    = "\033[97;46m"
	reset   = "\033[0m"
)
