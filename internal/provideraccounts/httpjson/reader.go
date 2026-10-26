package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

type Config struct {
	NativeUnit           string
	NativeUnitLabel      string
	BaseURL              string
	APIKey               string
	EndpointPath         string
	AuthorizationHeader  string
	AuthorizationType    string
	ResponseBalanceField string
	RequestTimeout       time.Duration
	ResponseMaxBytes     int64
	ErrorBodyMaxBytes    int64
	Client               *http.Client
}

type Reader struct {
	nativeUnit           string
	nativeUnitLabel      string
	endpoint             string
	apiKey               string
	authorizationHeader  string
	authorizationType    string
	responseBalanceField string
	requestTimeout       time.Duration
	responseMaxBytes     int64
	errorBodyMaxBytes    int64
	client               *http.Client
}

func New(config Config) (*Reader, error) {
	if config.NativeUnit != "" || config.NativeUnitLabel != "" {
		if err := (schema.NativeAmount{Unit: config.NativeUnit, UnitLabel: config.NativeUnitLabel, Amount: "0"}).Validate(); err != nil {
			return nil, errors.New("provider balance reader: invalid native unit")
		}
	}
	baseURL := strings.TrimSpace(config.BaseURL)
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil ||
		(parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") ||
		parsedBaseURL.Host == "" {
		return nil, errors.New("provider balance reader: invalid base URL")
	}
	if strings.TrimSpace(config.EndpointPath) == "" ||
		strings.TrimSpace(config.AuthorizationHeader) == "" ||
		strings.TrimSpace(config.ResponseBalanceField) == "" ||
		config.RequestTimeout <= 0 ||
		config.ResponseMaxBytes <= 0 ||
		config.ErrorBodyMaxBytes <= 0 {
		return nil, errors.New(
			"provider balance reader: incomplete configuration",
		)
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &Reader{
		nativeUnit:      config.NativeUnit,
		nativeUnitLabel: config.NativeUnitLabel,
		endpoint: strings.TrimRight(baseURL, "/") + "/" +
			strings.TrimLeft(config.EndpointPath, "/"),
		apiKey:               config.APIKey,
		authorizationHeader:  config.AuthorizationHeader,
		authorizationType:    strings.TrimSpace(config.AuthorizationType),
		responseBalanceField: config.ResponseBalanceField,
		requestTimeout:       config.RequestTimeout,
		responseMaxBytes:     config.ResponseMaxBytes,
		errorBodyMaxBytes:    config.ErrorBodyMaxBytes,
		client:               client,
	}, nil
}

func (reader *Reader) readBalance(ctx context.Context) (json.RawMessage, error) {
	callCtx, cancel := context.WithTimeout(ctx, reader.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		callCtx,
		http.MethodGet,
		reader.endpoint,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("provider balance request: %w", err)
	}
	if reader.apiKey != "" {
		value := reader.apiKey
		if reader.authorizationType != "" {
			value = reader.authorizationType + " " + value
		}
		request.Header.Set(reader.authorizationHeader, value)
	}
	response, err := reader.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("provider balance transport: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(
			io.Discard,
			io.LimitReader(response.Body, reader.errorBodyMaxBytes),
		)
		return nil, fmt.Errorf(
			"provider balance upstream status: %d",
			response.StatusCode,
		)
	}
	decoder := json.NewDecoder(io.LimitReader(
		response.Body,
		reader.responseMaxBytes,
	))
	var payload map[string]json.RawMessage
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("provider balance decode: %w", err)
	}
	// 支持点路径（如 "data.balance"）逐层定位嵌套字段
	field := payload
	found := true
	segments := strings.Split(reader.responseBalanceField, ".")
	for index, segment := range segments {
		raw, exists := field[segment]
		if !exists {
			found = false
			break
		}
		if index == len(segments)-1 {
			return raw, nil
		}
		if err := json.Unmarshal(raw, &field); err != nil {
			return nil, fmt.Errorf("provider balance field: %w", err)
		}
	}
	if !found {
		return nil, fmt.Errorf(
			"provider balance field: %q 不存在",
			reader.responseBalanceField,
		)
	}
	return nil, nil
}

func (reader *Reader) Balance(ctx context.Context) (float64, error) {
	raw, err := reader.readBalance(ctx)
	if err != nil {
		return 0, err
	}
	var balance float64
	if err := json.Unmarshal(raw, &balance); err != nil {
		return 0, fmt.Errorf("provider balance field: %w", err)
	}
	return balance, nil
}

// NativeBalance parses the original decimal token, never a float reconstruction.
func (reader *Reader) NativeBalance(ctx context.Context) (schema.NativeAmount, error) {
	if reader.nativeUnit == "" {
		return schema.NativeAmount{}, errors.New("native balance unit is not configured")
	}
	raw, err := reader.readBalance(ctx)
	if err != nil {
		return schema.NativeAmount{}, err
	}
	raw = bytes.TrimSpace(raw)
	amount := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &amount); err != nil {
			return schema.NativeAmount{}, errors.New("invalid native balance field")
		}
	}
	n, err := schema.ParseNativeAmount(amount)
	if err != nil {
		return schema.NativeAmount{}, errors.New("invalid native balance decimal")
	}
	return schema.NativeAmount{Unit: reader.nativeUnit, UnitLabel: reader.nativeUnitLabel, Amount: schema.FormatNativeAmount(n)}, nil
}
