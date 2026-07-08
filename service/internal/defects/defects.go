// Package defects stores user-submitted defect reports (wyrd pattern,
// wyrd/defects.py): full reproduction context + a required free-text
// reason, status lifecycle new → accepted | dismissed, triaged via the
// defects CLI. Blob fields are stored JSON-encoded under "payload" to
// dodge DynamoDB float / empty-string typing pitfalls.
package defects

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

const (
	StatusNew       = "new"
	StatusAccepted  = "accepted"
	StatusDismissed = "dismissed"
	StatusIndex     = "status-created_at-index"
	EnvTable        = "DEFECTS_TABLE"
)

// Report is a user-submitted defect with its reproduction context.
type Report struct {
	ID             string `json:"id"`
	CreatedAt      string `json:"created_at"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	CreatureGameID string `json:"creature_game_id"`
	CreatureName   string `json:"creature_name,omitempty"`
	Edition        string `json:"edition,omitempty"`
	SchemaVersion  string `json:"schema_version,omitempty"`
	FlaggedPath    string `json:"flagged_path,omitempty"`
	RenderedValue  string `json:"rendered_value,omitempty"`
	// TemplateStack is the deep-link state: [{g, s}] template game_ids +
	// selections — an accepted defect is replayable against current data.
	TemplateStack []map[string]any `json:"template_stack,omitempty"`
	TicketID      string           `json:"ticket_id,omitempty"`
	TriageNote    string           `json:"triage_note,omitempty"`
}

// Validate enforces the submission contract.
func (r *Report) Validate() error {
	if r.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	if r.CreatureGameID == "" {
		return fmt.Errorf("creature_game_id is required")
	}
	return nil
}

// Client wraps the DynamoDB table.
type Client struct {
	ddb   *dynamodb.Client
	table string
}

// NewClient builds a client from the environment (Lambda execution role or
// ambient credentials). Table from DEFECTS_TABLE.
func NewClient(ctx context.Context) (*Client, error) {
	table := os.Getenv(EnvTable)
	if table == "" {
		return nil, fmt.Errorf("%s is not set", EnvTable)
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	return &Client{ddb: dynamodb.NewFromConfig(cfg), table: table}, nil
}

// Put stores a new report, stamping id/created_at/status.
func (c *Client) Put(ctx context.Context, r *Report) error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.ID = uuid.NewString()
	r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	r.Status = StatusNew

	// blob fields JSON-encoded under payload (wyrd's DynamoDB lesson)
	payload, err := json.Marshal(map[string]any{
		"template_stack": r.TemplateStack,
		"rendered_value": r.RenderedValue,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	item := map[string]types.AttributeValue{
		"id":               &types.AttributeValueMemberS{Value: r.ID},
		"created_at":       &types.AttributeValueMemberS{Value: r.CreatedAt},
		"status":           &types.AttributeValueMemberS{Value: r.Status},
		"reason":           &types.AttributeValueMemberS{Value: r.Reason},
		"creature_game_id": &types.AttributeValueMemberS{Value: r.CreatureGameID},
		"payload":          &types.AttributeValueMemberS{Value: string(payload)},
	}
	for k, v := range map[string]string{
		"creature_name":  r.CreatureName,
		"edition":        r.Edition,
		"schema_version": r.SchemaVersion,
		"flagged_path":   r.FlaggedPath,
	} {
		if v != "" {
			item[k] = &types.AttributeValueMemberS{Value: v}
		}
	}
	_, err = c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.table),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("put defect: %w", err)
	}
	return nil
}

// ListByStatus queries the GSI, newest first.
func (c *Client) ListByStatus(ctx context.Context, status string, limit int32) ([]map[string]any, error) {
	out, err := c.ddb.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.table),
		IndexName:              aws.String(StatusIndex),
		KeyConditionExpression: aws.String("#s = :s"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": &types.AttributeValueMemberS{Value: status},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("query defects: %w", err)
	}
	items := make([]map[string]any, 0, len(out.Items))
	for _, it := range out.Items {
		items = append(items, plainItem(it))
	}
	return items, nil
}

// Get fetches one report by id.
func (c *Client) Get(ctx context.Context, id string) (map[string]any, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.table),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err != nil {
		return nil, fmt.Errorf("get defect: %w", err)
	}
	if out.Item == nil {
		return nil, nil
	}
	return plainItem(out.Item), nil
}

// SetStatus moves a report to accepted/dismissed with optional ticket/note.
func (c *Client) SetStatus(ctx context.Context, id, status, ticket, note string) error {
	if status != StatusAccepted && status != StatusDismissed {
		return fmt.Errorf("invalid status %q", status)
	}
	expr := "SET #s = :s"
	names := map[string]string{"#s": "status"}
	values := map[string]types.AttributeValue{
		":s": &types.AttributeValueMemberS{Value: status},
	}
	if ticket != "" {
		expr += ", ticket_id = :t"
		values[":t"] = &types.AttributeValueMemberS{Value: ticket}
	}
	if note != "" {
		expr += ", triage_note = :n"
		values[":n"] = &types.AttributeValueMemberS{Value: note}
	}
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(c.table),
		Key:                       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
		UpdateExpression:          aws.String(expr),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		ConditionExpression:       aws.String("attribute_exists(id)"),
	})
	if err != nil {
		return fmt.Errorf("update defect %s: %w", id, err)
	}
	return nil
}

// plainItem flattens DynamoDB string attributes and decodes the payload blob.
func plainItem(item map[string]types.AttributeValue) map[string]any {
	out := map[string]any{}
	for k, v := range item {
		if s, ok := v.(*types.AttributeValueMemberS); ok {
			out[k] = s.Value
		}
	}
	if raw, ok := out["payload"].(string); ok {
		var blob map[string]any
		if json.Unmarshal([]byte(raw), &blob) == nil {
			delete(out, "payload")
			for k, v := range blob {
				out[k] = v
			}
		}
	}
	return out
}
