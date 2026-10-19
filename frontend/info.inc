# Headers for the admin-only /api/<service>/info responses (included by nginx.conf).
# Not *.conf, so nginx doesn't load it as a server block on its own.
add_header X-Web-Pod $hostname;
add_header Cache-Control "no-store";
add_header X-Content-Type-Options nosniff always;
add_header X-Frame-Options DENY always;
add_header Referrer-Policy no-referrer always;
