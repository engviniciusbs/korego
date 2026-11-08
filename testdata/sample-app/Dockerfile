FROM nginx:1-alpine
COPY index.html /usr/share/nginx/html/index.html
HEALTHCHECK --interval=2s --timeout=2s --retries=5 CMD wget -qO- http://127.0.0.1/ || exit 1
