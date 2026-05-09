<img src="./assets/logo.png" width="40%" height="40%" align="right">

Caddy - Discord [![Discord](https://img.shields.io/discord/1063070457047289907.svg?label=&logo=discord&logoColor=ffffff&color=7389D8&labelColor=6A7EC2)](https://discord.gg/k9tVAwws8U)
====

tl;dr: Authenticate caddy routes based on a Discord User Identity with context of a specific guild (server).
_<br />e.g. Accessing `/really-cool-people` requires user to have `{Role}` within `{Guild}`_

This package contains a module allowing authorization in Caddy based on a Discord Identity, by using  Discords OAuth2 flow (authorization code grant).

---

> [!IMPORTANT]
> As of 22nd September 2025 versions v1.2.0 and below will fail with guild/role-based rules due to Discord API changes. Update to v1.2.1 or higher to solve. 

Licensed under [_GNU Affero General Public License v3.0_](https://github.com/enum-gg/caddy-discord/blob/main/LICENSE.md)
<br><i>Logo by [@AutonomousCat](https://github.com/AutonomousCat/)</i>

---

### Caddy Modules
```
caddydiscord
http.authentication.providers.discord
http.handler.discord
```

## Docker (Container)
```sh
docker run -p 8080:8080 \
  --rm -v $PWD/Caddyfile:/etc/caddy/Caddyfile \
  enumgg/caddy-discord:v1.2.1
```

## Discord Resources
**realm** allows you to name a label and group together specific targeted Discord Users by using the directives below.

| Resource        | Description                                                 | Example                                                                                                                                                                                                                          |
|-----------------|-------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| User ID         | Discord User IDs (_optionally with guild presence_)         | <pre>realm godmode {<br />  user 314009111187026172 # Allow user regardless of which guild they are in<br />  guild 1063070451111289907 {<br />    user 314009111187026199 # Allow user if they're part of guild<br />  }<br />} |
| Guild           | Any user that exists  _within the guild_                    | <pre>realm cool_guild_users {<br />  guild 1063070451111289907 {<br />    * # Allows all users <br />  }<br />}                                                                                                                  |
| Role            | Users that assigned a specific role _within a guild_        | <pre>realm cool_role {<br />  guild 1063070451111289907 {<br />    role 106301111332755034<br />    role 106301111332755034<br />  }<br />}</pre>                                                                                |

Loosely inspired from [caddy-security's Discord OAuth2 module](https://authp.github.io/docs/authenticate/oauth/backend-oauth2-0013-discord), with a much stronger focus on coupling Discord and Caddy for authentication purposes.

# Install

[**Download Latest Version**](https://github.com/enum-gg/caddy-discord/releases)

1. Download caddy + caddy-discord
    - Using released binaries
    - Build yourself using `xcaddy`
      - `xcaddy build --with github.com/enum-gg/caddy-discord`
2. Create Discord Application ([Discord Developer Portal](https://discord.com/developers/applications))
    - New Application
    - OAuth2
        - Obtain your Client ID & Client secret
        - Add Redirects [Docs](https://discord.com/developers/docs/topics/oauth2#authorization-code-grant-redirect-url-example)
3. Prepare your `Caddyfile`
    - Gather your Discord App OAuth2 Client ID & Client Secret,
    - Decide your route for caddy-discords to use as the OAuth2


## Caddyfile Example
```caddyfile
{
    discord {
        client_id 1000000000000000000 # Discord app OAuth client ID
        client_secret 8CEPZZZZZAfl_w19ZZZZW_k # Discord app OAuth secret
        redirect http://localhost:8080/discord/callback # Route you've configured with `discordauth callback`

        realm clique {
            guild 106307051119907 {
                role 10630111112755034
            }
        }

        realm just_for_me {
            user 31400111187026172
        }
    }
}

http://localhost:8080 {
    route /discord/callback {
         # Desigate route as OAuth callback endpoint
         discord callback
   }

    route /discordians-only {
         # Only allow discord users that auth against 'really_cool_area' realm
         protect using clique

         respond "Hello {http.auth.user.username}!<br /><br /><img src='https://cdn.discordapp.com/avatars/{http.auth.user.id}/{http.auth.user.avatar}?size=4096.png'> "
    }

    respond "Hello, world!"
}

```

## Building
```
xcaddy build --with github.com/enum-gg/caddy-discord=./
```

## Troubleshooting

### Enabling Debug Logging

caddy-discord emits debug messages through Caddy's built-in logging system. To see them, enable debug mode in your `Caddyfile` [global options block](https://caddyserver.com/docs/caddyfile/options#debug):

```caddyfile
{
    debug <-----

    discord {
        client_id     ...
        client_secret ...
        redirect      ...
    }
}
```

The `debug` directive lowers Caddy's default log level to `DEBUG`, which causes caddy-discord to emit a full trace of every Discord API call: the request URL, all request and response headers, the response status code, and the raw response body. This is the primary tool for diagnosing authentication failures.

### Log Levels

| Level   | What is logged                                                                                               |
|---------|--------------------------------------------------------------------------------------------------------------|
| `DEBUG` | Every outgoing Discord API request and its full response (URL, headers, body)                                |
| `INFO`  | Access denials (user authenticated but failed authorization rules like user ID, guild member, roles)         |
| `WARN`  | Recoverable issues: invalid/expired session cookie, guild membership fetch failures for member & role checks |
| `ERROR` | Internal failures: OAuth code exchange errors, token generation failures, unexpected Discord API responses   |

### Common Issues

#### Guild or role rules never match

A `WARN` log entry with message `failed to fetch guild membership, skipping rule` means Discord returned a non-200 response when checking guild membership. The `discord_message` and `discord_code` fields in the log will identify the exact Discord API error. Verify the guild ID in your realm config.

#### `Internal Error` response at the callback URL
A `DEBUG`-level `Discord API response` log will show the raw response body. Cross-reference `discord_code` values against the [Discord API error codes](https://discord.com/developers/docs/topics/opcodes-and-status-codes#json). Frequently will be the result of temporary Discord API outage.
