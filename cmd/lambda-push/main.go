// Command lambda-push pushes a message to a connection via the Management API.
// It is not attached to any route; the test client invokes it directly.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi/types"
)

type request struct {
	ConnectionID string `json:"connection_id"`
	Message      string `json:"message"`
}

type response struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	StatusCode int    `json:"status_code"`
}

func handler(ctx context.Context, req request) (response, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return response{}, err
	}
	c := apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
		o.BaseEndpoint = aws.String(os.Getenv("API_ENDPOINT"))
	})
	data, _ := json.Marshal(map[string]any{"type": "push", "message": req.Message, "ts": time.Now().UTC()})
	_, err = c.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
		ConnectionId: &req.ConnectionID, Data: data,
	})
	var gone *types.GoneException
	switch {
	case err == nil:
		return response{OK: true, StatusCode: 200}, nil
	case errors.As(err, &gone):
		return response{Error: "GoneException", StatusCode: 410}, nil
	default:
		return response{Error: err.Error(), StatusCode: 500}, nil
	}
}

func main() { lambda.Start(handler) }
