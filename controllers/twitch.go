package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/oauth2/clientcredentials"
	"golang.org/x/oauth2/twitch"
)

const (
	defaultClientID     = "abcd1234"
	defaultClientSecret = "abcd1234"
)

// TwitchStreamsResponse to marshal the json response from /helix/streams/
type TwitchStreamsResponse struct {
	Data []StreamData `json:"data"`
}

// StreamData to marshal the inner data of the TwitchStreamsResponse
type StreamData struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	GameID      string `json:"game_id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	ViewerCount int    `json:"viewer_count"`
	StartedAt   string `json:"started_at"`
}

// retrieve cached twitch access token from database and set in the
// Config struct. This is only called when the token is not set in Config
func (c *Controller) getCachedAccessToken() (string, error) {
	var tokenBytes []byte
	c.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("ConfigBucket"))
		tokenBytes = b.Get([]byte("twitchAccessToken"))
		return nil
	})
	if len(tokenBytes) < 1 {
		return "", errors.New("cached twitch access token not found in db")
	}
	return string(tokenBytes), nil
}

// update the cached access token record in the database
func (c *Controller) updateCachedAccessToken(accessToken string) error {
	var err error
	if accessToken == "" {
		return errors.New("updateCachedAccessToken: no token provided")
	}
	c.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("ConfigBucket"))
		err = b.Put([]byte("twitchAccessToken"), []byte(accessToken))
		return err
	})
	return nil
}

func validateAccessToken(accessToken string) error {
	r, err := http.NewRequest("GET", "https://id.twitch.tv/oauth2/validate", nil)
	if err != nil {
		log.Error(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "OAuth "+accessToken)

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return errors.New("token validation response status code != 200")
	}

	return nil
}

func (c *Controller) getNewAuthToken() error {
	var oauth2Config *clientcredentials.Config

	oauth2Config = &clientcredentials.Config{
		ClientID:     c.Config.TwitchClientID,
		ClientSecret: c.Config.TwitchClientSecret,
		TokenURL:     twitch.Endpoint.TokenURL,
	}

	token, err := oauth2Config.Token(context.Background())
	if err != nil {
		return err
	}

	log.Debug("New Access Token: ", token.AccessToken)
	err = c.updateCachedAccessToken(token.AccessToken)
	if err != nil {
		return err
	}
	return nil

}

func (c *Controller) validateClientCredentials() error {
	if c.Config.TwitchClientID == defaultClientID {
		err := errors.New("Default twitch client id value detected. Skipping twitch call")
		return err
	}
	if c.Config.TwitchClientSecret == defaultClientSecret {
		err := errors.New("Default twitch client secret value detected. Skipping twitch call")
		return err
	}
	return nil
}

//twitchAuthToken handles the lifecycle of the twitch access token
func (c *Controller) twitchAuthToken() (string, error) {
	var token string
	var err error

	token, err = c.getCachedAccessToken()
	if err != nil {
		return "", err
	}

	err = validateAccessToken(token)
	if err != nil {
		err = c.getNewAuthToken()
		if err != nil {
			return "", err
		}
	}

	token, err = c.getCachedAccessToken()
	if err != nil {
		return "", err
	}

	return token, nil
}

func (c *Controller) getStreams() ([]StreamData, error) {

	var err error
	var userQuery string

	err = c.validateClientCredentials()
	if err != nil {
		return nil, err
	}

	accessToken, err := c.twitchAuthToken()
	if err != nil {
		return nil, err
	}

	publishers, err := c.getAllPublisher()
	if err != nil {
		return nil, err
	}

	for i := range publishers {
		if publishers[i].TwitchStream == "" {
			continue
		}
		if userQuery != "" {
			userQuery = userQuery + "&"
		}
		userQuery = userQuery + fmt.Sprintf("user_login=%s", publishers[i].Name)
	}

	userStreamURL := "https://api.twitch.tv/helix/streams/?" + userQuery

	r, err := http.NewRequest("GET", userStreamURL, nil)
	if err != nil {
		log.Error(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("client-id", c.Config.TwitchClientID)
	r.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	streamResponse := TwitchStreamsResponse{}
	err = json.NewDecoder(resp.Body).Decode(&streamResponse)
	if err != nil {
		return nil, err
	}

	if len(streamResponse.Data) == 0 {
		log.Debug("no twitch streams currently live")
	}
	for i := range streamResponse.Data {
		log.Debug("Live Now:", streamResponse.Data[i].UserName)
	}

	return streamResponse.Data, nil
}
