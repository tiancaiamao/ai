#!/bin/bash
# Start web server on port 8080 serving content from /var/www/html

exec python3 -m http.server 8080 --directory /var/www/html