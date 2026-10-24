# Behind a reverse proxy

Pocket Watch is one HTTP service on port 8080. Video is long: a direct-played film is one request with byte
ranges for as long as it plays, a transcoded one is an HLS playlist and a stream of segments
(`/api/v1/media/…`). So the proxy must not buffer responses and must not time them out early. Set
`SECURE_COOKIES=true` behind HTTPS and `BASE_URL` to the public address.

## Caddy

```caddy
watch.example.com {
	reverse_proxy pocket-watch:8080 {
		flush_interval -1
	}
}
```

If iPhones stall on video behind Caddy, switch HTTP/3 off for the site (`protocols h1 h2`): AVPlayer and QUIC
don't always get along.

## nginx

```nginx
location / {
    proxy_pass http://pocket-watch:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 4h;
}
```

## Traefik

Nothing special: Traefik streams by default.

## Jellyfin

Pocket Watch talks to Jellyfin over the internal network (`JELLYFIN_URL`) and passes video through itself, so
Jellyfin doesn't need to be public for Pocket Watch. Keep it reachable for your TV and phone apps as before.
