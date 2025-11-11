package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"sybil-api/internal/shared"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// Get DSN from environment
	DSN, err := shared.SafeEnv("DSN")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: DSN environment variable is required: %v\n", err)
		os.Exit(1)
	}

	// Read migration file
	migrationPath := filepath.Join("migrations", "create_chat_history_table.sql")
	if len(os.Args) > 1 {
		migrationPath = os.Args[1]
	}

	migrationSQL, err := ioutil.ReadFile(migrationPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading migration file %s: %v\n", migrationPath, err)
		os.Exit(1)
	}

	// Connect to database
	db, err := sql.Open("mysql", DSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Test connection
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Error pinging database: %v\n", err)
		os.Exit(1)
	}

	// Split SQL by semicolons and execute each statement
	statements := strings.Split(string(migrationSQL), ";")
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}

		// Remove comments
		lines := strings.Split(stmt, "\n")
		var cleanLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "--") && trimmed != "" {
				cleanLines = append(cleanLines, line)
			}
		}
		stmt = strings.Join(cleanLines, "\n")

		if stmt == "" {
			continue
		}

		_, err := db.Exec(stmt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error executing statement: %v\n", err)
			fmt.Fprintf(os.Stderr, "Statement: %s\n", stmt)
			os.Exit(1)
		}
	}

	fmt.Println("Migration completed successfully!")
}
