# Stage 1: build frontend static assets
FROM node:20-alpine AS frontend-build
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: build backend binary
FROM golang:1.25-alpine AS backend-build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -o /obsboard ./cmd/obsboard

# Stage 3: minimal runtime image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
ARG TRIVY_VERSION=0.62.1
RUN curl -sfL \
    https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-64bit.tar.gz \
    | tar -xz -C /usr/local/bin trivy
COPY --from=backend-build /obsboard /obsboard
COPY --from=frontend-build /app/dist /frontend
ENV FRONTEND_DIR=/frontend
EXPOSE 4000
ENTRYPOINT ["/obsboard"]
