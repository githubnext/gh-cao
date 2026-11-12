package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/dashboard"
	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

func nativeMCP(cmd *cobra.Command) (err error) {
	if err := requireNativeSnapshot(cmd, true); err != nil {
		return err
	}
	catalog, err := loadNativeCatalog(nativeString(cmd, "dashboard"))
	if err != nil {
		return err
	}
	host, port := nativeString(cmd, "host"), 8765
	if host == "" {
		host = "127.0.0.1"
	}
	if cmd.Flags().Changed("port") {
		port, err = strconv.Atoi(nativeString(cmd, "port"))
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("--port must be a port number between 1 and 65535")
		}
	}
	handler, err := nativeMCPHandler(catalog, func(context.Context) (dashboardquery.CanonicalReader, func() error, error) {
		db, err := openNativeSnapshot(nativeSnapshotPath(cmd))
		if err != nil {
			return nil, nil, err
		}
		return db, db.Close, nil
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	advertised := host
	if host == "0.0.0.0" || host == "::" {
		advertised = "127.0.0.1"
	}
	address := "http://" + net.JoinHostPort(advertised, strconv.Itoa(port)) + "/mcp"
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "CAO MCP listening on %s\n", address); err != nil {
		return errors.Join(err, listener.Close())
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	select {
	case <-cmd.Context().Done():
		closeErr := server.Close()
		serveErr := <-finished
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(closeErr, serveErr)
		}
		if closeErr != nil {
			return closeErr
		}
		return cmd.Context().Err()
	case err := <-finished:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return printJSON(cmd, map[string]any{"command": "mcp", "url": address})
	}
}

func nativeRPCError(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

func nativeRPCResult(id, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func nativeMCPHandler(catalog *nativeCatalog, openReader func(context.Context) (dashboardquery.CanonicalReader, func() error, error)) (http.Handler, error) {
	contract, err := nativeReadJSON(dashboard.Catalog, "site/src/agent/mcp-contract.json")
	if err != nil {
		return nil, err
	}
	protocol := nativeText(contract["protocolVersion"])
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		send := func(status int, body any) {
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Cache-Control", "no-store")
			writer.WriteHeader(status)
			if err := json.NewEncoder(writer).Encode(body); err != nil {
				log.Print("Unable to write MCP response")
			}
		}
		if request.Method == http.MethodGet && request.URL.Path == "/healthz" {
			send(http.StatusOK, map[string]any{"status": "ok", "protocolVersion": protocol})
			return
		}
		if request.URL.Path != "/mcp" {
			send(http.StatusNotFound, nativeRPCError(nil, -32601, "Unknown endpoint; this server exposes POST /mcp"))
			return
		}
		if request.Method != http.MethodPost {
			send(http.StatusMethodNotAllowed, nativeRPCError(nil, -32600, "The MCP endpoint accepts POST requests"))
			return
		}
		if version := strings.TrimSpace(request.Header.Get("MCP-Protocol-Version")); version != protocol {
			message := "MCP-Protocol-Version header is required"
			if version != "" {
				message = "Unsupported MCP-Protocol-Version: " + version + "; this server implements " + protocol
			}
			send(http.StatusBadRequest, nativeRPCError(nil, -32600, message))
			return
		}
		content, err := io.ReadAll(io.LimitReader(request.Body, 65537))
		if err != nil {
			send(http.StatusInternalServerError, nativeRPCError(nil, -32603, "Internal error reading the request"))
			return
		}
		if len(content) > 65536 {
			send(http.StatusRequestEntityTooLarge, nativeRPCError(nil, -32600, "Request body exceeds 65536 bytes"))
			return
		}
		var decoded any
		if err := json.Unmarshal(content, &decoded); err != nil {
			send(http.StatusBadRequest, nativeRPCError(nil, -32700, "Request body is not valid JSON"))
			return
		}
		message := nativeObject(decoded)
		method, ok := message["method"].(string)
		if message["jsonrpc"] != "2.0" || !ok {
			send(http.StatusBadRequest, nativeRPCError(nil, -32600, "Request must be a JSON-RPC 2.0 object with a method"))
			return
		}
		var id any
		switch message["id"].(type) {
		case string, float64:
			id = message["id"]
		}
		header := strings.TrimSpace(request.Header.Get("Mcp-Method"))
		if header == "" || header != method {
			description := "Mcp-Method header is required"
			if header != "" {
				description = "Mcp-Method header " + header + " does not match request method " + method
			}
			send(http.StatusBadRequest, nativeRPCError(id, -32600, description))
			return
		}
		switch method {
		case "server/discover":
			send(http.StatusOK, nativeRPCResult(id, map[string]any{"protocolVersion": protocol, "serverInfo": contract["serverInfo"],
				"capabilities": map[string]any{"tools": map[string]any{}}, "instructions": contract["instructions"]}))
		case "tools/list":
			send(http.StatusOK, nativeRPCResult(id, map[string]any{"tools": contract["tools"], "cache": map[string]any{"ttlMs": 300000, "cacheScope": "private"}}))
		case "tools/call":
			params := nativeObject(message["params"])
			name := nativeText(params["name"])
			if header := strings.TrimSpace(request.Header.Get("Mcp-Name")); header != "" && header != name {
				send(http.StatusBadRequest, nativeRPCError(id, -32600, "Mcp-Name header "+header+" does not match tool "+name))
				return
			}
			if name != "cao_catalog" && name != "cao_query" {
				send(http.StatusBadRequest, nativeRPCError(id, -32602, "Unknown tool: "+name))
				return
			}
			arguments := map[string]any{}
			if value, exists := params["arguments"]; exists {
				arguments = nativeObject(value)
				if arguments == nil {
					send(http.StatusBadRequest, nativeRPCError(id, -32602, "Tool arguments must be an object"))
					return
				}
			}
			var payload map[string]any
			var err error
			if name == "cao_query" {
				var reader dashboardquery.CanonicalReader
				var closeReader func() error
				reader, closeReader, err = openReader(request.Context())
				if err == nil {
					payload, err = nativeMCPCall(request.Context(), catalog, reader, name, arguments)
					err = errors.Join(err, closeReader())
				}
			} else {
				payload, err = nativeMCPCall(request.Context(), catalog, nil, name, arguments)
			}
			if request.Context().Err() != nil {
				send(499, nativeRPCError(id, -32800, "Request cancelled"))
				return
			}
			result := map[string]any{}
			if err != nil {
				payload = map[string]any{"error": err.Error()}
				result["isError"] = true
			} else {
				result["structuredContent"] = payload
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				send(http.StatusInternalServerError, nativeRPCError(id, -32603, "Internal error executing the request"))
				return
			}
			result["content"] = []map[string]any{{"type": "text", "text": string(encoded)}}
			send(http.StatusOK, nativeRPCResult(id, result))
		default:
			send(http.StatusBadRequest, nativeRPCError(id, -32601, "Unknown method: "+method))
		}
	}), nil
}

func nativeMCPCall(ctx context.Context, catalog *nativeCatalog, db dashboardquery.CanonicalReader, name string, args map[string]any) (map[string]any, error) {
	id := nativeText(args["id"])
	if name == "cao_catalog" {
		if args["kind"] == "pages" {
			pages := catalog.describePages()
			if id != "" {
				for _, page := range pages {
					if page["id"] == id {
						return map[string]any{"command": "pages", "page": page}, nil
					}
				}
				return nil, nativeErrorf("Unknown dashboard page: %s", id)
			}
			return map[string]any{"command": "pages", "pages": pages}, nil
		}
		if args["kind"] == "queries" {
			if id != "" {
				if query := catalog.describeQuery(id); query != nil {
					return map[string]any{"command": "query-info", "query": query}, nil
				}
				return nil, nativeErrorf("Unknown dashboard query: %s", id)
			}
			queries := []map[string]any{}
			for _, query := range catalog.queries {
				queries = append(queries, catalog.describeQuery(nativeText(query["name"])))
			}
			return map[string]any{"command": "queries", "queries": queries}, nil
		}
		return nil, fmt.Errorf("cao_catalog requires kind to be \"pages\" or \"queries\"")
	}
	parameters := nativeObject(args["parameters"])
	if value, exists := args["parameters"]; exists && (value == nil || parameters == nil) {
		return nil, fmt.Errorf("cao_query parameters must be an object")
	}
	limit := 500
	if value, exists := args["limit"]; exists {
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) || number < 1 {
			return nil, fmt.Errorf("cao_query limit must be a positive integer")
		}
		limit = int(min(number, 5000))
	}
	return catalog.runQuery(ctx, db, id, parameters, limit)
}
