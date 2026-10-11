variable "REGISTRY" {
  default = "ghcr.io"
}

# Tag names without the image, separated by newlines or commas.
variable "TAGS" {
  default = "latest"
}

# Set to "true" in GitHub Actions to read and write the layer cache.
variable "GHA_CACHE" {
  default = ""
}

function "tags" {
  params = [image]
  result = formatlist("${REGISTRY}/${image}:%s", compact(split(",", replace(TAGS, "\n", ","))))
}

# Each image gets its own scope so builds don't overwrite each other's cache.
function "cache_from" {
  params = [scope]
  result = GHA_CACHE == "true" ? ["type=gha,scope=${scope}"] : []
}

function "cache_to" {
  params = [scope]
  result = GHA_CACHE == "true" ? ["type=gha,scope=${scope},mode=max"] : []
}

# Filled in with labels by docker/metadata-action.
target "docker-metadata-action" {}

target "_common" {
  inherits  = ["docker-metadata-action"]
  context   = "."
  platforms = ["linux/amd64", "linux/arm64"]
}

group "default" {
  targets = ["apps", "auth-web", "pico-ssh", "pgs-cdn", "pgs-standalone"]
}

target "apps" {
  name     = "${app}-${kind}"
  matrix = {
    app  = ["prose", "pastes", "pgs", "feeds", "pipe"]
    kind = ["web", "ssh"]
  }
  inherits   = ["_common"]
  target     = "release-${kind}"
  args       = { APP = app }
  tags       = tags("picosh/pico/${app}-${kind}")
  cache-from = cache_from("${app}-${kind}")
  cache-to   = cache_to("${app}-${kind}")
}

target "auth-web" {
  inherits   = ["_common"]
  target     = "release-web"
  args       = { APP = "auth" }
  tags       = tags("picosh/pico/auth-web")
  cache-from = cache_from("auth-web")
  cache-to   = cache_to("auth-web")
}

target "pico-ssh" {
  inherits   = ["_common"]
  target     = "release-ssh"
  args       = { APP = "pico" }
  tags       = tags("picosh/pico/pico-ssh")
  cache-from = cache_from("pico-ssh")
  cache-to   = cache_to("pico-ssh")
}

target "pgs-cdn" {
  inherits   = ["_common"]
  dockerfile = "Dockerfile.cdn"
  target     = "release-web"
  tags       = tags("picosh/pico/pgs-cdn")
  cache-from = cache_from("pgs-cdn")
  cache-to   = cache_to("pgs-cdn")
}

target "pgs-standalone" {
  inherits   = ["_common"]
  dockerfile = "Dockerfile.standalone"
  target     = "release"
  tags       = tags("picosh/pgs")
  cache-from = cache_from("pgs-standalone")
  cache-to   = cache_to("pgs-standalone")
}

# caddy and bouncer have their own workflows, so they stay out of the default group.
target "caddy" {
  inherits   = ["_common"]
  context    = "./caddy"
  tags       = tags("picosh/pico/caddy")
  cache-from = cache_from("caddy")
  cache-to   = cache_to("caddy")
}

target "bouncer" {
  inherits   = ["_common"]
  context    = "./bouncer"
  tags       = tags("picosh/pico/bouncer")
  cache-from = cache_from("bouncer")
  cache-to   = cache_to("bouncer")
}
