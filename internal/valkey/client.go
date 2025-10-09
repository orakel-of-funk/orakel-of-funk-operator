package valkey

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	valkey "github.com/valkey-io/valkey-go"

	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/recording"
)

type ValkeyClient struct {
	valkey.Client
}

func NewValKeyClient(valkeyHost, valkeyPort string) (*ValkeyClient, error) {

	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{valkeyHost + ":" + valkeyPort}})
	if err != nil {
		return nil, err
	}

	// Ensure the client is connected
	if err = client.Do(context.TODO(), client.B().Ping().Build()).Error(); err != nil {
		return nil, err
	}

	return &ValkeyClient{client}, nil
}

func (v *ValkeyClient) storeEntry(ctx context.Context, key string, value string) error {

	// Store the entry with a key and value, setting an expiration time of 1 day (24 hours)
	err := v.Client.Do(ctx, v.Client.B().Set().Key(strings.ToLower(key)).Value(value).Ex(24*time.Hour).Build()).Error()
	if err != nil {
		return err
	}

	return nil
}

func (v *ValkeyClient) getEntry(ctx context.Context, key string) (string, error) {

	res, err := v.Client.Do(ctx, v.Client.B().Get().Key(strings.ToLower(key)).Build()).AsBytes()
	if err != nil {
		return "", err
	}

	if len(res) == 0 {
		return "", nil // No entry found for the given key
	}
	return string(res), nil
}

func (v ValkeyClient) deleteEntry(ctx context.Context, key string) error {

	err := v.Client.Do(ctx, v.Client.B().Del().Key(strings.ToLower(key)).Build()).Error()
	if err != nil {
		return err
	}

	return nil
}

func (v ValkeyClient) StoreRecording(ctx context.Context, suffix string, workloadRecording *recording.WorkloadRecording) error {
	key := "recording:" + suffix + ":" + workloadRecording.Type

	// Store the metrics in Valkey
	jsonB, err := json.Marshal(workloadRecording)
	if err != nil {
		return err
	}

	return v.storeEntry(ctx, key, string(jsonB))
}

func (v ValkeyClient) GetRecording(ctx context.Context, keySuffix string) (*recording.WorkloadRecording, error) {

	key := "recording:" + keySuffix

	value, err := v.getEntry(ctx, key)
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, nil // No recording found for the given key
	}

	var workloadRecording recording.WorkloadRecording
	err = json.Unmarshal([]byte(value), &workloadRecording)
	if err != nil {
		return nil, err
	}
	return &workloadRecording, nil

}

func (v ValkeyClient) DeleteRecording(ctx context.Context, keySuffix, recordingType string) error {
	key := "recording:" + keySuffix + ":" + recordingType

	return v.deleteEntry(ctx, key)
}
