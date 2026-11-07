// Command lambda-ws handles $connect, $disconnect and $default for stack B.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Package-level state: lives and dies with this execution environment.
var (
	envID   = randID()
	counter int64
	bootAt  = time.Now().UTC()

	awsCfg aws.Config
	ddb    *dynamodb.Client
	table  = os.Getenv("TABLE_NAME")

	mgmtMu sync.Mutex
	mgmt   = map[string]*apigatewaymanagementapi.Client{}
)

type reply struct {
	Type         string    `json:"type"`
	EnvID        string    `json:"env_id"`
	LogStream    string    `json:"log_stream"`
	RequestID    string    `json:"request_id"`
	ConnectionID string    `json:"connection_id"`
	DBName       string    `json:"db_name"`
	Counter      int64     `json:"counter"`
	EnvBootAt    time.Time `json:"env_boot_at"`
	Seq          int64     `json:"seq"`
	Ts           time.Time `json:"ts"`
}

func init() {
	var err error
	awsCfg, err = config.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(err)
	}
	ddb = dynamodb.NewFromConfig(awsCfg)
}

func handler(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	rc := req.RequestContext
	ok := events.APIGatewayProxyResponse{StatusCode: 200}

	switch rc.RouteKey {
	case "$connect":
		name := req.QueryStringParameters["name"]
		_, err := ddb.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: &table,
			Item: map[string]types.AttributeValue{
				"connectionId": &types.AttributeValueMemberS{Value: rc.ConnectionID},
				"name":         &types.AttributeValueMemberS{Value: name},
				"ttl":          &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)},
			},
		})
		if err != nil {
			return events.APIGatewayProxyResponse{StatusCode: 500}, err
		}
		slog.Info("connect", "env_id", envID, "connection_id", rc.ConnectionID, "name", name)

	case "$disconnect":
		_, err := ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: &table,
			Key:       map[string]types.AttributeValue{"connectionId": &types.AttributeValueMemberS{Value: rc.ConnectionID}},
		})
		slog.Info("disconnect", "env_id", envID, "connection_id", rc.ConnectionID, "err", err)

	default:
		var in struct {
			Seq     int64 `json:"seq"`
			SleepMS int   `json:"sleep_ms"`
		}
		_ = json.Unmarshal([]byte(req.Body), &in)
		if in.SleepMS > 2000 {
			in.SleepMS = 2000
		}

		out, err := ddb.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: &table,
			Key:       map[string]types.AttributeValue{"connectionId": &types.AttributeValueMemberS{Value: rc.ConnectionID}},
		})
		if err != nil {
			return events.APIGatewayProxyResponse{StatusCode: 500}, err
		}
		dbName := ""
		if v, found := out.Item["name"].(*types.AttributeValueMemberS); found {
			dbName = v.Value
		}

		counter++
		if in.SleepMS > 0 { // hold the environment busy so concurrent messages need another one
			time.Sleep(time.Duration(in.SleepMS) * time.Millisecond)
		}
		reqID := ""
		if lc, found := lambdacontext.FromContext(ctx); found {
			reqID = lc.AwsRequestID
		}
		r := reply{
			Type: "reply", EnvID: envID, LogStream: lambdacontext.LogStreamName, RequestID: reqID,
			ConnectionID: rc.ConnectionID, DBName: dbName, Counter: counter, EnvBootAt: bootAt,
			Seq: in.Seq, Ts: time.Now().UTC(),
		}
		b, _ := json.Marshal(r)
		slog.Info("reply", "env_id", envID, "connection_id", rc.ConnectionID, "counter", counter, "db_name", dbName)
		_, err = client("https://" + rc.DomainName + "/" + rc.Stage).PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
			ConnectionId: &rc.ConnectionID, Data: b,
		})
		if err != nil {
			slog.Error("post_to_connection", "err", err)
			return events.APIGatewayProxyResponse{StatusCode: 500}, err
		}
	}
	return ok, nil
}

func client(endpoint string) *apigatewaymanagementapi.Client {
	mgmtMu.Lock()
	defer mgmtMu.Unlock()
	if c, found := mgmt[endpoint]; found {
		return c
	}
	c := apigatewaymanagementapi.NewFromConfig(awsCfg, func(o *apigatewaymanagementapi.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	mgmt[endpoint] = c
	return c
}

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func main() { lambda.Start(handler) }
