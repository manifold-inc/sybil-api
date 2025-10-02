// Package search includes all routes and functionality for Sybil Search
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"sybil-api/internal/shared"

	"go.uber.org/zap"
	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"
)

type SearchManager struct {
	GoogleSearchEngineID string
	GoogleAPIKey         string
	GoogleService        *customsearch.Service
	GoogleACURL          string
}

func NewSearchManager() (*SearchManager, []error) {
	var errs []error

	googleSearchEngineID, err := shared.SafeEnv("GOOGLE_SEARCH_ENGINE_ID")
	if err != nil {
		errs = append(errs, err)
	}

	googleAPIKey, err := shared.SafeEnv("GOOGLE_API_KEY")
	if err != nil {
		errs = append(errs, err)
	}

	googleACURL, err := shared.SafeEnv("GOOGLE_AC_URL")
	if err != nil {
		errs = append(errs, err)
	}
	googleService, err := customsearch.NewService(context.Background(), option.WithAPIKey(googleAPIKey))
	if err != nil {
		return nil, []error{errors.New("failed to connect to google service")}
	}

	if len(errs) != 0 {
		return nil, errs
	}

	return &SearchManager{
		GoogleSearchEngineID: googleSearchEngineID,
		GoogleAPIKey:         googleAPIKey,
		GoogleService:        googleService,
		GoogleACURL:          googleACURL,
	}, nil

}

func QueryGoogleSearch(googleService *customsearch.Service, Log *zap.SugaredLogger, googleSearchEngineID string, query string, page int, searchType ...string) (*shared.SearchResponseBody, error) {
	search := googleService.Cse.List().Q(query).Cx(googleSearchEngineID)

	if len(searchType) > 0 && searchType[0] == "image" {
		search = search.SearchType("image")
	}

	if page > 1 {
		search = search.Start(int64(page-1)*10 + 1)
	}

	res, err := search.Do()
	if err != nil {
		Log.Errorf("Google Search Error: %s", err.Error())
		return nil, err
	}

	results := make([]shared.SearchResults, len(res.Items))
	for i, item := range res.Items {
		title := item.Title
		content := item.Snippet
		link := item.Link
		imgSource := link // Default to link if no specific image source found
		source := ""
		resolution := ""
		metadata := ""
		publishedDate := ""

		// Handle pagemap for image source and metadata if available
		if item.Pagemap != nil {
			var pagemap map[string]any
			if err := json.Unmarshal(item.Pagemap, &pagemap); err != nil {
				Log.Errorf("Failed to unmarshal pagemap: %s", err.Error())
				continue
			}

			// Handle image search results
			if len(searchType) > 0 && searchType[0] == "image" {
				// Try cse_image first
				if cseImages, ok := pagemap["cse_image"].([]any); ok {
					if cseImage := shared.GetFirstMap(cseImages); cseImage != nil {
						src, res := extractImageData(cseImage, "src")
						if src != "" {
							imgSource = src
						}
						if res != "" {
							resolution = res
						}
					}
				}

				// Try imageobject for additional metadata
				if imageObjects, ok := pagemap["imageobject"].([]any); ok {
					if imageObject := shared.GetFirstMap(imageObjects); imageObject != nil {
						src, res := extractImageData(imageObject, "url")
						if src != "" {
							imgSource = src
						}
						if res != "" {
							resolution = res
						}
						if content := shared.GetString(imageObject, "content"); content != "" {
							metadata = content
						}
					}
				}
			}

			// Handle metadata from metatags
			if metatags, ok := pagemap["metatags"].([]any); ok {
				if metatag := shared.GetFirstMap(metatags); metatag != nil {
					publishedDate = shared.GetString(metatag, "article:published_time")
					if desc := shared.GetString(metatag, "og:description"); desc != "" {
						metadata = desc
					}
				}
			}
		}

		// Get source and parsed URL from link
		if link != "" {
			if parsed, err := url.Parse(link); err == nil {
				source = parsed.Hostname()
				parsedURL := strings.Split(source, ".")
				results[i] = shared.SearchResults{
					Title:         &title,
					Content:       &content,
					URL:           &link,
					ImgSource:     &imgSource,
					ParsedURL:     &parsedURL,
					Source:        &source,
					Resolution:    &resolution,
					Metadata:      &metadata,
					PublishedDate: &publishedDate,
				}
				continue
			}
		}

		// Fallback for when URL parsing fails
		emptyParsedURL := []string{}
		results[i] = shared.SearchResults{
			Title:         &title,
			Content:       &content,
			URL:           &link,
			ImgSource:     &imgSource,
			ParsedURL:     &emptyParsedURL,
			Source:        &source,
			Resolution:    &resolution,
			Metadata:      &metadata,
			PublishedDate: &publishedDate,
		}
	}

	// Create and return SearchResponseBody
	totalResults, err := strconv.Atoi(res.SearchInformation.TotalResults)
	if err != nil {
		Log.Errorf("Error converting total results to int: %s", err.Error())
		totalResults = 0
	}

	// Get related queries
	suggestions := []string{}
	relatedSearch := googleService.Cse.List().Q("related:" + query).Cx(googleSearchEngineID)
	relatedRes, err := relatedSearch.Do()
	if err == nil && relatedRes.Items != nil {
		for _, item := range relatedRes.Items {
			if item.Title != "" {
				suggestions = append(suggestions, item.Title)
			}
		}
	}

	return &shared.SearchResponseBody{
		Query:           query,
		NumberOfResults: totalResults,
		Results:         results,
		Suggestions:     suggestions,
	}, nil
}

func extractImageData(m map[string]any, srcKey string) (string, string) {
	src := shared.GetString(m, srcKey)
	width := shared.GetString(m, "width")
	height := shared.GetString(m, "height")
	if width != "" && height != "" {
		return src, fmt.Sprintf("%sx%s", width, height)
	}
	return src, ""
}
