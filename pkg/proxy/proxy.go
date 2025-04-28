package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mymcp/mcp-proxy/pkg/common"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ProxyServer struct {
	*server.MCPServer
	BaseUrl    string
	BasePath   string
	Status     string
	Error      string
	ServerInfo mcp.Implementation
	Client     client.MCPClient
	Config     *common.McpServerConfig
}

func NewProxyServer(cfg *common.McpServerConfig) (*ProxyServer, error) {
	proxyServer := &ProxyServer{
		Config: cfg,
	}

	err := proxyServer.initClient()
	if err != nil {
		proxyServer.Status = "error"
		proxyServer.Error = err.Error()
	} else {
		proxyServer.MCPServer = server.NewMCPServer(proxyServer.ServerInfo.Name, proxyServer.ServerInfo.Version)
		proxyServer.Status = "started"
	}

	return proxyServer, nil
}

func (p *ProxyServer) initClient() error {
	var err error
	if p.Config.Transport == "stdio" {
		stdioClient, err := client.NewStdioMCPClient(p.Config.Cmd, p.Config.Env, p.Config.Args...)
		if err != nil {
			return err
		}
		p.Client = stdioClient
	} else if p.Config.Transport == "sse" {
		url := p.Config.Url
		sseTransport, err := transport.NewSSE(url, transport.WithHeaders(p.Config.Headers))
		if err != nil {
			return err
		}
		sseClient := client.NewClient(sseTransport)
		sseClient.Start(context.Background())
		p.Client = sseClient
	} else {
		return errors.New("invalid transport type")
	}
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-proxy",
		Version: "1.0.0",
	}
	initializeResult, err := p.Client.Initialize(context.Background(), initRequest)
	if err != nil {
		return err
	}
	p.ServerInfo = initializeResult.ServerInfo
	return nil
}

// fails on the request.
type UnparseableMessageError struct {
	message json.RawMessage
	method  mcp.MCPMethod
	err     error
}

func (e *UnparseableMessageError) Error() string {
	return fmt.Sprintf("unparseable %s request: %s", e.method, e.err)
}

func (e *UnparseableMessageError) Unwrap() error {
	return e.err
}

func (e *UnparseableMessageError) GetMessage() json.RawMessage {
	return e.message
}

func (e *UnparseableMessageError) GetMethod() mcp.MCPMethod {
	return e.method
}

// RequestError is an error that can be converted to a JSON-RPC error.
// Implements Unwrap() to allow inspecting the error chain.
type requestError struct {
	id   any
	code int
	err  error
}

func (e *requestError) Error() string {
	return fmt.Sprintf("request error: %s", e.err)
}

func (e *requestError) ToJSONRPCError() mcp.JSONRPCError {
	return mcp.JSONRPCError{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      e.id,
		Error: struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    any    `json:"data,omitempty"`
		}{
			Code:    e.code,
			Message: e.err.Error(),
		},
	}
}

func (e *requestError) Unwrap() error {
	return e.err
}

// serverKey is the context key for storing the server instance
type serverKey struct{}

type BaseMessage struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  mcp.MCPMethod `json:"method"`
	ID      any           `json:"id,omitempty"`
}

// HandleMessage processes an incoming JSON-RPC message and returns an appropriate response
func (s *ProxyServer) HandleMessage(
	ctx context.Context,
	message json.RawMessage,
) mcp.JSONRPCMessage {
	// Add server to context
	ctx = context.WithValue(ctx, serverKey{}, s)
	var reqErr *requestError

	baseMessage := BaseMessage{}
	if err := json.Unmarshal(message, &baseMessage); err != nil {
		return createErrorResponse(
			nil,
			mcp.PARSE_ERROR,
			"Failed to parse message",
		)
	}

	// Check for valid JSONRPC version
	if baseMessage.JSONRPC != mcp.JSONRPC_VERSION {
		return createErrorResponse(
			baseMessage.ID,
			mcp.INVALID_REQUEST,
			"Invalid JSON-RPC version",
		)
	}

	if baseMessage.ID == nil {
		var notification mcp.JSONRPCNotification
		if err := json.Unmarshal(message, &notification); err != nil {
			return createErrorResponse(
				nil,
				mcp.PARSE_ERROR,
				"Failed to parse notification",
			)
		}
		// s.handleNotification(ctx, notification)
		return nil // Return nil for notifications
	}

	switch baseMessage.Method {
	case mcp.MethodInitialize:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.InitializeRequest) (*mcp.InitializeResult, error) {
			return s.Client.Initialize(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	case mcp.MethodPing:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.PingRequest) (*mcp.EmptyResult, error) {
			e := s.Client.Ping(ctx)
			return &mcp.EmptyResult{}, e
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, result)
	case mcp.MethodResourcesList:
		var result *mcp.ListResourcesResult
		result, reqErr = requestClient(baseMessage, message, func(request *mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
			return s.Client.ListResources(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	case mcp.MethodResourcesTemplatesList:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.ListResourceTemplatesRequest) (*mcp.ListResourceTemplatesResult, error) {
			return s.Client.ListResourceTemplates(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	case mcp.MethodResourcesRead:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return s.Client.ReadResource(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	case mcp.MethodPromptsList:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
			return s.Client.ListPrompts(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	case mcp.MethodPromptsGet:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return s.Client.GetPrompt(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	case mcp.MethodToolsList:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
			return s.Client.ListTools(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	case mcp.MethodToolsCall:
		result, reqErr := requestClient(baseMessage, message, func(request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.Client.CallTool(ctx, *request)
		})
		if reqErr != nil {
			return reqErr.ToJSONRPCError()
		}
		return createResponse(baseMessage.ID, *result)
	default:
		return createErrorResponse(
			baseMessage.ID,
			mcp.METHOD_NOT_FOUND,
			fmt.Sprintf("Method %s not found", baseMessage.Method),
		)
	}
}

func requestClient[T any, R any](baseMessage BaseMessage, message json.RawMessage, handler func(*T) (*R, error)) (*R, *requestError) {
	var request T
	var reqErr *requestError
	var result *R
	var err error
	if unmarshalErr := json.Unmarshal(message, &request); unmarshalErr != nil {
		reqErr = &requestError{
			id:   baseMessage.ID,
			code: mcp.INVALID_REQUEST,
			err:  &UnparseableMessageError{message: message, err: unmarshalErr, method: baseMessage.Method},
		}
	} else {
		fmt.Println(request)
		result, err = handler(&request)
		if err != nil {
			reqErr = &requestError{
				id:   baseMessage.ID,
				code: mcp.INVALID_REQUEST,
				err:  &UnparseableMessageError{message: message, err: unmarshalErr, method: baseMessage.Method},
			}
		}
	}
	if reqErr != nil {
		return nil, reqErr
	}
	return result, nil
}

func createResponse(id interface{}, result interface{}) mcp.JSONRPCMessage {
	return mcp.JSONRPCResponse{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      id,
		Result:  result,
	}
}
