FROM --platform=$BUILDPLATFORM hugomods/hugo:latest AS build
WORKDIR /src/web
COPY web/ /src/web/
COPY README.md /src/README.md
COPY CHANGELOG.md /src/CHANGELOG.md
RUN hugo --minify

FROM nginx:alpine
RUN apk upgrade --no-cache
COPY --from=build /src/web/public /usr/share/nginx/html
EXPOSE 80
