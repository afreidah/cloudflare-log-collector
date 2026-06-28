FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS godoc
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN go install github.com/princjef/gomarkdoc/cmd/gomarkdoc@latest
# Discover packages dynamically (identical to the Makefile's web-godoc target)
# so the two generators can never drift out of sync.
RUN mkdir -p /godoc && \
    for dir in $(go list -f '{{.Dir}}' ./internal/...); do \
        pkg=$(basename "$dir"); \
        desc=$(go list -f '{{.Doc}}' ./internal/$pkg | sed 's/"/\\"/g'); \
        printf -- '---\ntitle: "%s"\ndescription: "%s"\n---\n\n' "$pkg" "$desc" > /godoc/$pkg.md && \
        gomarkdoc ./internal/$pkg >> /godoc/$pkg.md && \
        sed -i '/^# '"$pkg"'$/d' /godoc/$pkg.md; \
    done

FROM --platform=$BUILDPLATFORM hugomods/hugo:0.163.3 AS build
WORKDIR /src/web
COPY web/ /src/web/
COPY README.md /src/README.md
COPY CHANGELOG.md /src/CHANGELOG.md
COPY --from=godoc /godoc/ /src/web/content/godoc/
COPY web/content/godoc/_index.md /src/web/content/godoc/_index.md
RUN hugo --minify

# Serve the static site with the unprivileged nginx image: it runs as a non-root
# user (UID 101) and listens on 8080, which satisfies docker:S6471. apk upgrade
# needs root, so switch to root only for that step, then drop back to 101. NOTE:
# the fronting proxy/service must target 8080 instead of 80.
FROM nginxinc/nginx-unprivileged:alpine
USER root
RUN apk upgrade --no-cache
USER 101
COPY --from=build /src/web/public /usr/share/nginx/html
EXPOSE 8080
