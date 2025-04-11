package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/aidarkhanov/nanoid"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"

	"github.com/redis/go-redis/v9"
)

var (
	HOTKEY                      string
	PUBLIC_KEY                  string
	PRIVATE_KEY                 string
	ENDON_URL                   string
	SEARX_URL                   string
	INSTANCE_UUID               string
	DSN                         string
	DEBUG                       bool
	TARGON_HUB_ENDPOINT         string
	TARGON_HUB_ENDPOINT_API_KEY string
	GOOGLE_SEARCH_ENGINE_ID     string
	GOOGLE_API_KEY              string

	db            *sql.DB
	client        *redis.Client
	googleService *customsearch.Service
)

var Reset = "\033[0m"
var Red = "\033[31m"
var Green = "\033[32m"
var Yellow = "\033[33m"
var Blue = "\033[34m"
var Purple = "\033[35m"
var Cyan = "\033[36m"
var Gray = "\033[37m"
var White = "\033[97m"

type Context struct {
	echo.Context
	Info *log.Logger
	Warn *log.Logger
	Err  *log.Logger
}

func main() {
	HOTKEY = safeEnv("HOTKEY")
	PUBLIC_KEY = safeEnv("PUBLIC_KEY")
	PRIVATE_KEY = safeEnv("PRIVATE_KEY")
	ENDON_URL = safeEnv("ENDON_URL")
	DSN = safeEnv("DSN")
	SEARX_URL = getEnv("SEARX_URL", "http://searxng:8080/")
	TARGON_HUB_ENDPOINT = safeEnv("TARGON_HUB_ENDPOINT")
	TARGON_HUB_ENDPOINT_API_KEY = safeEnv("TARGON_HUB_ENDPOINT_API_KEY")
	INSTANCE_UUID = uuid.New().String()
	GOOGLE_SEARCH_ENGINE_ID = safeEnv("GOOGLE_SEARCH_ENGINE_ID")
	GOOGLE_API_KEY = safeEnv("GOOGLE_API_KEY")
	debug, present := os.LookupEnv("DEBUG")
	if !present {
		DEBUG = false
	} else {
		DEBUG, _ = strconv.ParseBool(debug)
	}

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			reqId, _ := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 12)
			InfoLog := log.New(os.Stdout, fmt.Sprintf("%sINFO [%s]: %s", Green, reqId, Reset), log.Ldate|log.Ltime|log.Lshortfile)
			WarnLog := log.New(os.Stdout, fmt.Sprintf("%sWARNING [%s]: %s", Yellow, reqId, Reset), log.Ldate|log.Ltime|log.Lshortfile)
			ErrLog := log.New(os.Stdout, fmt.Sprintf("%sERROR [%s]: %s", Red, reqId, Reset), log.Ldate|log.Ltime|log.Lshortfile)
			cc := &Context{c, InfoLog, WarnLog, ErrLog}
			return next(cc)
		}
	})
	e.Use(middleware.Recover())
	// wth GET, PUT, POST or DELETE method.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete},
	}))

	client = redis.NewClient(&redis.Options{
		Addr:     "redis:6379",
		Password: "",
		DB:       0,
	})
	var err error
	db, err = sql.Open("mysql", DSN)
	if err != nil {
		log.Fatal("failed to open db connection", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("failed to ping: %v", err)
	}
	googleService, err = customsearch.NewService(context.Background(), option.WithAPIKey(GOOGLE_API_KEY))
	if err != nil {
		log.Fatalf("failed to create google service: %v", err)
	}
	defer db.Close()
	defer client.Close()

	e.GET(("/ping"), func(c echo.Context) error {
		return c.String(200, "")
	})
	e.POST("/search/images", func(c echo.Context) error {
		cc := c.(*Context)
		type RequestBody struct {
			Query string `json:"query"`
			Page  int    `json:"page"`
		}
		var requestBody RequestBody
		err = json.NewDecoder(c.Request().Body).Decode(&requestBody)
		query := requestBody.Query
		if err != nil {
			sendErrorToEndon(err, "/search/images")
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		cc.Info.Printf("/search/images: %s, page: %d\n", query, requestBody.Page)
		search, err := queryGoogleSearch(cc, query, requestBody.Page, "image")
		if err != nil {
			sendErrorToEndon(err, "/search/images")
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(200, search.Results)
	})

	e.POST("/search", func(c echo.Context) error {
		cc := c.(*Context)
		type RequestBody struct {
			Query string `json:"query"`
			Model string `json:"model"`
		}
		cc.Request().Header.Add("Content-Type", "application/json")
		var requestBody RequestBody
		err = json.NewDecoder(c.Request().Body).Decode(&requestBody)
		if err != nil {
			log.Println("Error decoding json")
			sendErrorToEndon(err, "/search")
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		query := requestBody.Query
		model := requestBody.Model
		if len(query) == 0 {
			cc.Warn.Println("No query")
			sendErrorToEndon(fmt.Errorf("no query"), "/search")
			return echo.NewHTTPError(http.StatusBadRequest, "No query found")
		}

		cc.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		cc.Response().Header().Set("Cache-Control", "no-cache")
		cc.Response().Header().Set("Connection", "keep-alive")
		cc.Response().Header().Set("X-Accel-Buffering", "no")

		cc.Info.Printf("/search: %s\n", query)
		cc.Info.Printf("Model: %s\n", model)

		general, err := queryGoogleSearch(cc, query, 1, "web")
		if err != nil {
			sendErrorToEndon(err, "/search")
			return c.String(500, "")
		}

		sendEvent(cc, map[string]any{
			"type":    "sources",
			"sources": general.Results,
		})
		sendEvent(cc, map[string]any{
			"type":      "related",
			"followups": general.Suggestions,
		})

		llmSources := []string{}
		if len(general.Results) != 0 {
			herocard := general.Results[0]
			llmSources = append(llmSources, fmt.Sprintf("Title: %s:\nSnippet: %s\n", derefString(general.Results[0].Title), derefString(general.Results[0].Content)))
			sendEvent(cc, map[string]any{
				"type": "heroCard",
				"heroCard": map[string]any{
					"type":  "news",
					"url":   *herocard.Url,
					"image": herocard.Thumbnail,
					"title": *herocard.Title,
					"intro": *herocard.Content,
					"size":  "auto",
				},
			})
		}

		//answer := queryTargon(cc, llmSources, query)
		answer := queryFallbacks(cc, llmSources, query, model)
		// We let this run in the background
		go saveAnswer(query, answer, llmSources, c.Request().Header.Get("X-SESSION-ID"))

		cc.Info.Println("Finished")
		return c.String(200, "")
	})

	e.GET("/search/autocomplete", func(c echo.Context) error {
		cc := c.(*Context)
		query := c.QueryParam("q")
		if query == "" {
			return c.JSON(200, []string{})
		}

		suggestions, err := queryGoogleAutocomplete(cc, query)
		if err != nil {
			sendErrorToEndon(err, "/search/autocomplete")
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.JSON(200, suggestions)
	})

	e.POST("/search/sources", func(c echo.Context) error {
		cc := c.(*Context)
		type RequestBody struct {
			Query string `json:"query"`
			Page  int    `json:"page"`
		}
		var requestBody RequestBody
		err = json.NewDecoder(c.Request().Body).Decode(&requestBody)
		query := requestBody.Query
		if err != nil {
			sendErrorToEndon(err, "/search/sources")
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		cc.Info.Printf("/search/sources: %s, page: %d\n", query, requestBody.Page)
		search, err := queryGoogleSearch(cc, query, requestBody.Page, "web")
		if err != nil {
			sendErrorToEndon(err, "/search/sources")
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(200, search.Results)
	})
	e.Logger.Fatal(e.Start(":80"))
}
