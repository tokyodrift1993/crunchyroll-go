package crunchyroll

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// LOCALE represents a locale / language
type LOCALE string

const (
	JP LOCALE = "ja-JP"
	US        = "en-US"
	LA        = "es-LA"
	ES        = "es-ES"
	FR        = "fr-FR"
	BR        = "pt-BR"
	IT        = "it-IT"
	DE        = "de-DE"
	RU        = "ru-RU"
	ME        = "ar-ME"
)

type Crunchyroll struct {
	// Client is the http.Client to perform all requests over
	Client *http.Client
	// Locale specifies in which language all results should be returned / requested
	Locale LOCALE
	// SessionID is the crunchyroll session id which was used for authentication
	SessionID string

	// Config stores parameters which are needed by some api calls
	Config struct {
		TokenType   string
		AccessToken string

		CountryCode    string
		Premium        bool
		Channel        string
		Policy         string
		Signature      string
		KeyPairID      string
		AccountID      string
		ExternalID     string
		MaturityRating string
	}
}

// LoginWithCredentials logs in via crunchyroll email and password
func LoginWithCredentials(email string, password string, locale LOCALE, client *http.Client) (*Crunchyroll, error) {
	sessionIDEndpoint := fmt.Sprintf("https://api.crunchyroll.com/start_session.0.json?version=1.0&access_token=%s&device_type=%s&device_id=%s",
		"LNDJgOit5yaRIWN", "com.crunchyroll.windows.desktop", "Az2srGnChW65fuxYz2Xxl1GcZQgtGgI")
	sessResp, err := client.Get(sessionIDEndpoint)
	if err != nil {
		return nil, err
	}
	defer sessResp.Body.Close()

	var data map[string]interface{}
	body, _ := ioutil.ReadAll(sessResp.Body)
	json.Unmarshal(body, &data)

	sessionID := data["data"].(map[string]interface{})["session_id"].(string)

	loginEndpoint := "https://api.crunchyroll.com/login.0.json"
	authValues := url.Values{}
	authValues.Set("session_id", sessionID)
	authValues.Set("account", email)
	authValues.Set("password", password)
	client.Post(loginEndpoint, "application/x-www-form-urlencoded", bytes.NewBufferString(authValues.Encode()))

	return LoginWithSessionID(sessionID, locale, client)
}

// LoginWithSessionID logs in via a crunchyroll session id.
// Session ids are automatically generated as a cookie when visiting https://www.crunchyroll.com
func LoginWithSessionID(sessionID string, locale LOCALE, client *http.Client) (*Crunchyroll, error) {
	crunchy := &Crunchyroll{
		Client:    client,
		Locale:    locale,
		SessionID: sessionID,
	}
	var endpoint string
	var err error
	var resp *http.Response
	var jsonBody map[string]interface{}

	// start session
	endpoint = fmt.Sprintf("https://api.crunchyroll.com/start_session.0.json?session_id=%s",
		sessionID)
	resp, err = client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&jsonBody)
	if _, ok := jsonBody["message"]; ok {
		return nil, errors.New("invalid session id")
	}
	data := jsonBody["data"].(map[string]interface{})

	crunchy.Config.CountryCode = data["country_code"].(string)
	user := data["user"]
	if user == nil {
		return nil, errors.New("invalid session id, user is not logged in")
	}
	if user.(map[string]interface{})["premium"] == "" {
		crunchy.Config.Premium = false
		crunchy.Config.Channel = "-"
	} else {
		crunchy.Config.Premium = true
		crunchy.Config.Channel = "crunchyroll"
	}

	var etpRt string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "etp_rt" {
			etpRt = cookie.Value
			break
		}
	}

	// token
	endpoint = "https://beta-api.crunchyroll.com/auth/v1/token"
	grantType := url.Values{}
	grantType.Set("grant_type", "etp_rt_cookie")

	authRequest, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(grantType.Encode()))
	if err != nil {
		return nil, err
	}
	authRequest.Header.Add("Authorization", "Basic bm9haWhkZXZtXzZpeWcwYThsMHE6")
	authRequest.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	authRequest.AddCookie(&http.Cookie{
		Name:  "session_id",
		Value: sessionID,
	})
	authRequest.AddCookie(&http.Cookie{
		Name:  "etp_rt",
		Value: etpRt,
	})

	resp, err = client.Do(authRequest)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&jsonBody)
	crunchy.Config.TokenType = jsonBody["token_type"].(string)
	crunchy.Config.AccessToken = jsonBody["access_token"].(string)

	// index
	endpoint = "https://beta-api.crunchyroll.com/index/v2"
	resp, err = crunchy.request(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&jsonBody)
	cms := jsonBody["cms"].(map[string]interface{})

	crunchy.Config.Policy = cms["policy"].(string)
	crunchy.Config.Signature = cms["signature"].(string)
	crunchy.Config.KeyPairID = cms["key_pair_id"].(string)

	// me
	endpoint = "https://beta-api.crunchyroll.com/accounts/v1/me"
	resp, err = crunchy.request(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&jsonBody)

	crunchy.Config.AccountID = jsonBody["account_id"].(string)
	crunchy.Config.ExternalID = jsonBody["external_id"].(string)

	//profile
	endpoint = "https://beta-api.crunchyroll.com/accounts/v1/me/profile"
	resp, err = crunchy.request(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&jsonBody)

	crunchy.Config.MaturityRating = jsonBody["maturity_rating"].(string)

	return crunchy, nil
}

// request is a base function which handles api requests
func (c *Crunchyroll) request(endpoint string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", fmt.Sprintf("%s %s", c.Config.TokenType, c.Config.AccessToken))

	resp, err := c.Client.Do(req)
	if err == nil {
		bodyAsBytes, _ := ioutil.ReadAll(resp.Body)
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, &AccessError{
				URL:  endpoint,
				Body: bodyAsBytes,
			}
		} else {
			var errStruct struct {
				Message string `json:"message"`
			}
			json.NewDecoder(bytes.NewBuffer(bodyAsBytes)).Decode(&errStruct)
			if errStruct.Message != "" {
				return nil, &AccessError{
					URL:     endpoint,
					Body:    bodyAsBytes,
					Message: errStruct.Message,
				}
			}
		}
		resp.Body = ioutil.NopCloser(bytes.NewBuffer(bodyAsBytes))
	}
	return resp, err
}

// Search searches a query and returns all found series and movies within the given limit
func (c *Crunchyroll) Search(query string, limit uint) (s []*Series, m []*Movie, err error) {
	searchEndpoint := fmt.Sprintf("https://beta-api.crunchyroll.com/content/v1/search?q=%s&n=%d&type=&locale=%s",
		query, limit, c.Locale)
	resp, err := c.request(searchEndpoint)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var jsonBody map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&jsonBody)

	for _, item := range jsonBody["items"].([]interface{}) {
		item := item.(map[string]interface{})
		if item["total"].(float64) > 0 {
			switch item["type"] {
			case "series":
				for _, series := range item["items"].([]interface{}) {
					series2 := &Series{
						crunchy: c,
					}
					if err := decodeMapToStruct(series, series2); err != nil {
						return nil, nil, err
					}
					if err := decodeMapToStruct(series.(map[string]interface{})["series_metadata"].(map[string]interface{}), series2); err != nil {
						return nil, nil, err
					}

					s = append(s, series2)
				}
			case "movie_listing":
				for _, movie := range item["items"].([]interface{}) {
					movie2 := &Movie{
						crunchy: c,
					}
					if err := decodeMapToStruct(movie, movie2); err != nil {
						return nil, nil, err
					}

					m = append(m, movie2)
				}
			}
		}
	}

	return s, m, nil
}

// FindVideo fins a Video (Season or Movie) by a crunchyroll link
// e.g. https://www.crunchyroll.com/darling-in-the-franxx
func (c *Crunchyroll) FindVideo(seriesUrl string) (Video, error) {
	pattern := regexp.MustCompile(`(?m)^https?://(www\.)?crunchyroll\.com(/\w{2}(-\w{2})?)?/(?P<series>[^/]+)/?$`)
	if urlMatch := pattern.FindAllStringSubmatch(seriesUrl, -1); len(urlMatch) != 0 {
		groups := regexGroups(urlMatch, pattern.SubexpNames()...)
		title, ok := groups["series"]
		if !ok {
			return nil, errors.New("series could not be found")
		}

		s, m, err := c.Search(title, 1)
		if err != nil {
			return nil, err
		}

		if len(s) > 0 {
			return s[0], nil
		} else if len(m) > 0 {
			return m[0], nil
		}
		return nil, errors.New("no series or movie could be found")
	}

	return nil, errors.New("invalid url")
}

// FindEpisode finds an episode by its crunchyroll link
// e.g. https://www.crunchyroll.com/darling-in-the-franxx/episode-1-alone-and-lonesome-759575
func (c *Crunchyroll) FindEpisode(url string) ([]*Episode, error) {
	pattern := regexp.MustCompile(`(?m)^https?://(www\.)?crunchyroll\.com(/\w{2}(-\w{2})?)?/(?P<series>[^/]+)/episode-\d+-(?P<title>\D+).*`)
	if urlMatch := pattern.FindAllStringSubmatch(url, -1); len(urlMatch) != 0 {
		groups := regexGroups(urlMatch, pattern.SubexpNames()...)
		var slugTitle string
		var ok bool
		if slugTitle, ok = groups["title"]; !ok {
			return nil, errors.New("invalid url")
		}
		slugTitle = strings.TrimSuffix(slugTitle, "-")
		video, err := c.FindVideo(fmt.Sprintf("https://www.crunchyroll.com/%s", groups["series"]))
		if err != nil {
			return nil, err
		}
		seasons, err := video.(*Series).Seasons()
		if err != nil {
			return nil, err
		}

		var matchingEpisodes []*Episode
		for _, season := range seasons {
			episodes, err := season.Episodes()
			if err != nil {
				return nil, err
			}
			for _, episode := range episodes {
				if episode.SlugTitle == slugTitle {
					matchingEpisodes = append(matchingEpisodes, episode)
				}
			}
		}
		return matchingEpisodes, nil
	}

	return nil, errors.New("invalid url")
}
