package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/redis/go-redis/v9"
)

var (
	HOTKEY        string
	IP            string
	PUBLIC_KEY    string
	PRIVATE_KEY   string
	NEWS          string
	SEARCH        string
	IMAGE         string
	INSTANCE_UUID string
	DSN           string

	db *sql.DB
)

func main() {
	HOTKEY = safeEnv("HOTKEY")
	IP = safeEnv("EXTERNAL_IP")
	PUBLIC_KEY = safeEnv("PUBLIC_KEY")
	PRIVATE_KEY = safeEnv("PRIVATE_KEY")
	DSN = safeEnv("DSN")
	NEWS = "https://google.serper.dev/news"
	SEARCH = "https://google.serper.dev/search"
	IMAGE = "https://google.serper.dev/images"
	INSTANCE_UUID = uuid.New().String()

	e := echo.New()
	client := redis.NewClient(&redis.Options{
		Addr:     "cache:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
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
	e.POST("/search", func(c echo.Context) (err error) {
		c.Request().Header.Add("Content-Type", "application/json")
		var requestBody RequestBody
		err = json.NewDecoder(c.Request().Body).Decode(&requestBody)
		if err != nil {
			log.Println("Error decoding json")
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		query := requestBody.Query
		if len(query) == 0 {
			log.Println("No query")
			return echo.NewHTTPError(http.StatusBadRequest, "No query found")
		}
		log.Println(query)

		c.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().Header().Set("X-Accel-Buffering", "no")
		c.Response().WriteHeader(http.StatusOK)

		sources := make(chan []string)
		answer := make(chan string)
		var wg sync.WaitGroup
		wg.Add(4)
		go querySearch(&wg, c, query, sources)
		go queryNews(&wg, c, query)
		go queryImages(&wg, c, query)
		go queryMiners(&wg, c, client, sources, query, answer)
		go saveAnswer(c, query, answer, sources, c.Request().Header.Get("X-SESSION-ID"))
		wg.Wait()
		return c.String(200, "")
	})
	e.Logger.Fatal(e.Start(":80"))
}
