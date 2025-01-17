GREEN  := "\\u001b[32m"
RESET  := "\\u001b[0m\\n"
CHECK  := "\\xE2\\x9C\\x94"

org := 'manifoldlabs'
set shell := ["bash", "-uc"]

set dotenv-required

default:
  @just --list

build dockerfile = "" opts = "":
  docker compose {{dockerfile}} build {{opts}}
  @printf " {{GREEN}}{{CHECK}} Successfully built! {{CHECK}} {{RESET}}"

pull:
  @git pull

up extra='': (build "-f docker-compose.yml -f docker-compose.dev.yml")
  docker compose -f docker-compose.dev.yml up -d {{extra}}
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


k8s-up: k8s-create k8s-build k8s-load k8s-deploy

k8s-create:
  kind create cluster --config ./k8s-deployment/local-kind-config.yaml

k8s-build:
  docker buildx build -t manifoldlabs/sybil-api:dev api --platform linux/amd64,linux/arm64

k8s-load:
  kind load docker-image manifoldlabs/sybil-api:dev

k8s-deploy:
  kubectl apply -f ./k8s-deployment/config-map.yaml
  envsubst < ./k8s-deployment/deployments.yaml | kubectl apply -f -

k8s-delete:
  kubectl delete -f ./k8s-deployment/deployments.yaml
  kubectl delete -f ./k8s-deployment/config-map.yaml

k8s-down:
  kind delete cluster
