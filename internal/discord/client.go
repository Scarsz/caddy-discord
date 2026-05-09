package discord

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"go.uber.org/zap"
)

type APIClient struct {
	client *http.Client
	logger *zap.Logger
}

func (d *APIClient) getRequest(rawURL string) (*http.Response, error) {
	return d.client.Get(rawURL)
}

func NewClientWrapper(client *http.Client, logger *zap.Logger) *APIClient {
	return &APIClient{
		client: client,
		logger: logger,
	}
}

func (d *APIClient) FetchCurrentUser() (*User, error) {
	return fetch[User](d, "https://discord.com/api/users/@me")
}

func (d *APIClient) FetchGuildMembership(guildID string) (*GuildMemberResponse, error) {
	return fetch[GuildMemberResponse](d, fmt.Sprintf("https://discord.com/api/users/@me/guilds/%s/member", url.QueryEscape(guildID)))
}

func fetch[T any](client *APIClient, rawURL string) (*T, error) {
	client.logger.Debug("Discord API request", zap.String("url", rawURL))

	response, err := client.getRequest(rawURL)
	if err != nil {
		client.logger.Error("Discord API request failed", zap.String("url", rawURL), zap.Error(err))
		return nil, err
	}

	var bodyBytes []byte
	if response.Body != nil {
		bodyBytes, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			client.logger.Error("failed to read Discord API response body",
				zap.String("url", rawURL),
				zap.Int("status", response.StatusCode),
				zap.Error(err),
			)
			return nil, err
		}
		response.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	logFields := []zap.Field{
		zap.String("url", rawURL),
		zap.Int("status", response.StatusCode),
		zap.Any("response_headers", response.Header),
		zap.ByteString("body", bodyBytes),
	}
	if response.Request != nil {
		logFields = append(logFields, zap.Any("request_headers", response.Request.Header))
	}
	client.logger.Debug("Discord API response", logFields...)

	if response.StatusCode == http.StatusOK {
		normalised, err := getBody[T](response)
		if err != nil {
			client.logger.Error("failed to unmarshal Discord API response",
				zap.String("url", rawURL),
				zap.ByteString("body", bodyBytes),
				zap.Error(err),
			)
			return nil, err
		}
		return normalised, nil
	}

	normalisedError, err := getBody[ErrorResponse](response)
	if err != nil {
		client.logger.Error("failed to parse Discord API error response body",
			zap.String("url", rawURL),
			zap.Int("status", response.StatusCode),
			zap.Error(err),
		)
		return nil, err
	}

	// Invalid requests
	// https://discord.com/developers/docs/topics/rate-limits#invalid-request-limit-aka-cloudflare-bans
	// https://discord.com/developers/docs/topics/opcodes-and-status-codes#http
	if response.StatusCode == http.StatusUnauthorized {
		client.logger.Warn("Discord API: unauthorized (token expired?)",
			zap.String("url", rawURL),
			zap.String("discord_message", normalisedError.Message),
			zap.Int("discord_code", int(normalisedError.Code)),
		)
		return nil, ErrInsufficientScope
	}

	if response.StatusCode == http.StatusForbidden {
		client.logger.Warn("Discord API: forbidden (insufficient scope?)",
			zap.String("url", rawURL),
			zap.String("discord_message", normalisedError.Message),
			zap.Int("discord_code", int(normalisedError.Code)),
		)
		return nil, ErrInsufficientScope
	}

	// Rate limited
	// https://discord.com/developers/docs/topics/rate-limits#rate-limits
	if response.StatusCode == http.StatusTooManyRequests {
		client.logger.Warn("Discord API: rate limited",
			zap.String("url", rawURL),
			zap.String("discord_message", normalisedError.Message),
			zap.Int("discord_code", int(normalisedError.Code)),
		)
		return nil, ErrRateLimited
	}

	client.logger.Error("Discord API: unexpected error response",
		zap.String("url", rawURL),
		zap.Int("status", response.StatusCode),
		zap.String("discord_message", normalisedError.Message),
		zap.Int("discord_code", int(normalisedError.Code)),
	)
	return nil, resolveError(normalisedError.Code)
}
