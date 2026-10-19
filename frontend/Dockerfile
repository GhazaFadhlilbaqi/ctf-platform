# nginx-unprivileged runs as a non-root user (uid 101) and listens on 8080.
FROM nginxinc/nginx-unprivileged:1.30-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY info.inc /etc/nginx/conf.d/info.inc
COPY public/ /usr/share/nginx/html/

# Version label and accent colour shown in the UI. Build v2 with a different
# colour so the rolling update is obvious on screen:
#   docker build --build-arg APP_VERSION=v2 --build-arg ACCENT='#e0a83b' .
ARG APP_VERSION=v1
ARG ACCENT=#d0604a

USER root
RUN printf '{"version":"%s","accent":"%s"}\n' "$APP_VERSION" "$ACCENT" \
      > /usr/share/nginx/html/version.json
USER 101

EXPOSE 8080
