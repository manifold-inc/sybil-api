package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/aidarkhanov/nanoid"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/redis/go-redis/v9"
)

var (
	HOTKEY        string
	PUBLIC_KEY    string
	PRIVATE_KEY   string
	NEWS          string
	SEARCH        string
	IMAGE         string
	INSTANCE_UUID string
	DSN           string
	DEBUG         bool

	db     *sql.DB
	client *redis.Client
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
	DSN = safeEnv("DSN")
	NEWS = "https://google.serper.dev/news"
	SEARCH = "https://google.serper.dev/search"
	IMAGE = "https://google.serper.dev/images"
	INSTANCE_UUID = uuid.New().String()
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
	client = redis.NewClient(&redis.Options{
		Addr:     "cache:6379",
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
	defer db.Close()

	e.POST("/search", func(c echo.Context) error {
		cc := c.(*Context)
		type RequestBody struct {
			Query string   `json:"query"`
			Files []string `json:"files"`
		}
		cc.Request().Header.Add("Content-Type", "application/json")
		var requestBody RequestBody
		err = json.NewDecoder(c.Request().Body).Decode(&requestBody)
		if err != nil {
			log.Println("Error decoding json")
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		query := requestBody.Query
		if len(query) == 0 {
			cc.Warn.Println("No query")
			return echo.NewHTTPError(http.StatusBadRequest, "No query found")
		}

		c.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().Header().Set("X-Accel-Buffering", "no")

		cc.Info.Printf("/search: %s\n", query)
		sources := make(chan []string)
		answer := make(chan string)
		var wg sync.WaitGroup
		wg.Add(3)
		go querySearch(&wg, cc, query, sources, 0)
		go queryNews(&wg, cc, query)
		//go queryImages(&wg, c, query)
		go queryMiners(&wg, cc, client, sources, query, answer)
		go saveAnswer(cc, query, answer, sources, c.Request().Header.Get("X-SESSION-ID"))
		wg.Wait()
		return c.String(200, "")
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
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		cc.Info.Printf("/search/sources: %s, page: %d\n", query, requestBody.Page)
		search, err := querySerper(cc, query, SEARCH, requestBody.Page)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		sources := parseSources(search)
		return c.JSON(200, sources)
	})
	e.Logger.Fatal(e.Start(":80"))
}
