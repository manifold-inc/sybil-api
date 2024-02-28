package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
)

func main() {
	e := echo.New()
	client := redis.NewClient(&redis.Options{
		Addr:     "cache:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	e.GET("/miners", func(c echo.Context) error {
		ctx := context.Background()
		userSession := client.JSONGet(ctx, "miners").Val()
		fmt.Println(userSession)
		return c.JSON(200, userSession)
	})
	e.GET("/", func(c echo.Context) error {
		c.Response().Header().Set("Access-Control-Allow-Origin", "*")
		c.Response().Header().Set("Access-Control-Expose-Headers", "Content-Type")

		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		for i := 0; i < 10; i++ {
			fmt.Fprintf(c.Response(), "data: %s\n\n", fmt.Sprintf("Event %d", i))
			time.Sleep(2 * time.Second)
			c.Response().Flush()
		}
		return c.NoContent(http.StatusOK)
	})
	e.Logger.Fatal(e.Start(":80"))
}
