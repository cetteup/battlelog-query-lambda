package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/rs/zerolog/log"
)

var (
	ErrTooManyRequests = errors.New("too many requests")
)

var handler *Handler

func init() {
	user := NewUser(os.Getenv("EA_USERNAME"), os.Getenv("EA_SESSION_ID"))
	if err := user.Authenticate(context.Background()); err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed to authenticate user")
	}

	log.Info().Msgf("Authenticated as %s", user.Username)

	handler = NewHandler(user)
}

func main() {
	lambda.Start(handler.HandleRequest)
}

type Handler struct {
	user *User
}

func NewHandler(user *User) *Handler {
	return &Handler{
		user: user,
	}
}

func (h *Handler) HandleRequest(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.HTTP.Method != http.MethodGet {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: http.StatusMethodNotAllowed,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: `{"error": "Method Not Allowed"}`,
		}, nil
	}

	nick := strings.TrimSpace(req.QueryStringParameters["nick"])
	if nick == "" {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: http.StatusBadRequest,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: `{"error": "Bad Request"}`,
		}, nil
	}

	b, err := h.search(ctx, nick)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to search for player")

		if errors.Is(err, ErrTooManyRequests) {
			return events.APIGatewayV2HTTPResponse{
				StatusCode: http.StatusTooManyRequests,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: `{"error": "Too Many Requests"}`,
			}, nil
		}

		return events.APIGatewayV2HTTPResponse{
			StatusCode: http.StatusInternalServerError,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: `{"error": "Internal Server Error"}`,
		}, nil
	}

	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusOK,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: string(b),
	}, nil
}

func (h *Handler) search(ctx context.Context, nick string) ([]byte, error) {
	v := url.Values{}
	v.Set("query", nick)
	v.Set("post-check-sum", h.user.GetPostChecksum())

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://battlelog.battlefield.com/bf4/search/query/",
		strings.NewReader(v.Encode()),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/83.0.4103.116 Safari/537.36")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")

	resp, err := h.user.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrTooManyRequests
	} else if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("request to %s failed with status code: %d", req.URL.String(), resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	data := struct {
		Message string `json:"message"`
	}{}
	if err = json.Unmarshal(b, &data); err != nil {
		return nil, err
	}

	// Battlelog does not allow bursts of searches
	// The rate limit seems strict (2-3 searches within a few seconds), but refills quickly (few seconds)
	if data.Message == "TOOMANYSEARCHES" {
		return nil, ErrTooManyRequests
	}

	return b, nil
}

type User struct {
	Username string

	client *http.Client
}

func NewUser(username string, sid string) *User {
	// Ignoring always nil error (see https://github.com/golang/go/issues/18685)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(&url.URL{
		Scheme: "https",
		Host:   "accounts.ea.com",
	}, []*http.Cookie{
		{
			Name:     "sid",
			Value:    sid,
			Path:     "/connect",
			Domain:   ".ea.com",
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteNoneMode,
		},
	})

	return &User{
		Username: username,
		client: &http.Client{
			Jar: jar,
		},
	}
}

func (u *User) Authenticate(ctx context.Context) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://accounts.ea.com/connect/auth?client_id=battlelog&response_type=code&state=mohw&redirect_uri=https%3A%2F%2Fbattlelog.battlefield.com%2Fsso%2F%3Ftokentype%3Dcode&prompt=none&release_type=prod",
		http.NoBody,
	)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/83.0.4103.116 Safari/537.36")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// state=mohw in the above URL causes Battlelog to redirect to /mohw/user/[username]
	// giving us a very nice indicator to check whether authentication was successful
	if resp.Request.URL.Path != fmt.Sprintf("/mohw/user/%s/", u.Username) {
		if resp.Request.Response != nil && resp.Request.Response.Request.URL.Query().Has("error_code") {
			return fmt.Errorf("unexpected redirect to %s", resp.Request.Response.Request.URL.String())
		}
		return fmt.Errorf("unexpected redirect to %s", resp.Request.URL.String())
	}

	return nil
}

func (u *User) GetPostChecksum() string {
	for _, cookie := range u.client.Jar.Cookies(&url.URL{
		Scheme: "https",
		Host:   "battlelog.battlefield.com",
	}) {
		// Post checksum is just the first 10 characters of the beaker session id
		if cookie.Name == "beaker.session.id" {
			return cookie.Value[:min(10, len(cookie.Value))]
		}
	}

	return ""
}

func (u *User) Do(req *http.Request) (*http.Response, error) {
	return u.client.Do(req)
}
