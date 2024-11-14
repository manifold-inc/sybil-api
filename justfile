GREEN  := "\\u001b[32m"
RESET  := "\\u001b[0m\\n"
CHECK  := "\\xE2\\x9C\\x94"

org := 'manifoldlabs'
set shell := ["bash", "-uc"]

default:
  @just --list

build dockerfile = "" opts = "":
  docker compose {{dockerfile}} build {{opts}}
  @printf " {{GREEN}}{{CHECK}} Successfully built! {{CHECK}} {{RESET}}"

pull:
  @git pull

up extra='': (build "-f docker-compose.yml -f docker-compose.dev.yml")
  docker compose -f docker-compose.dev.yml up {{extra}}
  @printf " {{GREEN}}{{CHECK}} Images Started {{CHECK}} {{RESET}}"

prod image:
  docker compose pull 
  docker rollout {{image}}
  @printf " {{GREEN}}{{CHECK}} Images Started {{CHECK}} {{RESET}}"

upgrade: pull build
  docker compose up -d searcher mcacher
  @printf " {{GREEN}}{{CHECK}} Images Started {{CHECK}} {{RESET}}"

down:
  @docker compose -f docker-compose.dev.yml down

push: (build)
  export VERSION=$(git rev-parse --short HEAD) && docker compose -f docker-compose.build.yml build
  export VERSION=$(git rev-parse --short HEAD) && docker compose -f docker-compose.build.yml push
  docker compose -f docker-compose.build.yml build
  docker compose -f docker-compose.build.yml push

