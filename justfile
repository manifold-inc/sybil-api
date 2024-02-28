GREEN  := "\\u001b[32m"
RESET  := "\\u001b[0m\\n"
CHECK  := "\\xE2\\x9C\\x94"

set shell := ["bash", "-uc"]

default:
  @just --list

build opts = "":
  docker compose build {{opts}}
  @printf " {{GREEN}}{{CHECK}} Successfully built! {{CHECK}} {{RESET}}"

up: build
  docker compose up -d searcher mcacher
  @printf " {{GREEN}}{{CHECK}} Images Started {{CHECK}} {{RESET}}"

down:
  @docker compose down

print:
  @printf " {{GREEN}}{{CHECK}} Images Started {{CHECK}} {{RESET}}"
