package caddydiscord

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"time"

	"github.com/Scarsz/caddy-discord/internal/discord"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

var (
	_ caddy.Provisioner           = (*DiscordAuthPlugin)(nil)
	_ caddyhttp.MiddlewareHandler = (*DiscordAuthPlugin)(nil)
	_ caddy.Validator             = (*DiscordAuthPlugin)(nil)
)

func init() {
	caddy.RegisterModule(DiscordAuthPlugin{})
	httpcaddyfile.RegisterHandlerDirective("discord", parseCaddyfileHandlerDirective)
}

func parseCaddyfileHandlerDirective(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var s DiscordAuthPlugin
	s.UnmarshalCaddyfile(h.Dispenser)
	return s, s.UnmarshalCaddyfile(h.Dispenser)
}

// DiscordAuthPlugin is used in combination with
// http.authentication.providers.discord to provide an authentication
// layer based on a Discord identity.
//
// See https://caddyserver.com/docs/modules/http.authentication.providers.discord
// or https://github.com/enum-gg/caddy-discord
type DiscordAuthPlugin struct {
	Configuration   []string
	OAuth           *oauth2.Config
	Realms          *RealmRegistry
	Key             string
	tokenSigner     TokenSignerSignature
	flowTokenParser FlowTokenParserSignature
	cookie          CookieNamer
	logger          *zap.Logger
}

func (DiscordAuthPlugin) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.discord",
		New: func() caddy.Module { return new(DiscordAuthPlugin) },
	}
}

func (s *DiscordAuthPlugin) Provision(ctx caddy.Context) error {
	s.logger = ctx.Logger(s)

	ctxApp, _ := ctx.App(moduleName)
	app := ctxApp.(*DiscordPortalApp)

	s.OAuth = app.getOAuthConfig()
	s.cookie = CookieName(app.ExecutionKey)
	s.Realms = &app.Realms

	key, err := hex.DecodeString(app.Key)
	if err != nil {
		return err
	}

	s.tokenSigner = NewTokenSigner(key)
	s.flowTokenParser = NewFlowTokenParser(key)

	return nil
}

func (s *DiscordAuthPlugin) Validate() error {
	return nil
}

// UnmarshalCaddyfile will extract discordauth directives on a server-level
//
//	route /some/path/callback {
//	    discordauth callback
//	}
func (s *DiscordAuthPlugin) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	s.Configuration = []string{}

	for d.Next() {
		if d.NextArg() {
			if d.Val() == "callback" {
				s.Configuration = append(s.Configuration, d.Val())

				if d.NextArg() {
					return d.ArgErr()
				}
			}
		}
	}

	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (d DiscordAuthPlugin) ServeHTTP(w http.ResponseWriter, r *http.Request, _ caddyhttp.Handler) error {
	ctx := context.Background()
	q := r.URL.Query()

	token, err := d.flowTokenParser(q.Get("state"))
	if err != nil {
		d.logger.Error("failed to parse OAuth state parameter; session lost (load balancer or server restart?)", zap.Error(err))
		http.Error(w, "Internal Error", http.StatusInternalServerError)
		return err
	}

	realm := d.Realms.ByName(token.Realm)
	if realm == nil {
		d.logger.Error("realm not found", zap.String("realm", token.Realm))
		http.Error(w, "Internal Error", http.StatusInternalServerError)
		return err
	}

	tok, err := d.OAuth.Exchange(ctx, q.Get("code"))
	if err != nil {
		d.logger.Error("OAuth authorization code exchange failed", zap.String("realm", token.Realm), zap.Error(err))
		return err
	}

	client := discord.NewClientWrapper(d.OAuth.Client(ctx, tok), d.logger)

	allowed := false

	identity, err := client.FetchCurrentUser()
	if err != nil || len(identity.ID) == 0 {
		d.logger.Error("failed to fetch Discord user identity", zap.String("realm", realm.Ref), zap.Error(err))
		http.Error(w, "Failed to resolve Discord User", http.StatusInternalServerError)
		return err
	}

	for _, rule := range realm.Identifiers {
		d.logger.Debug("request",
			zap.String("realm", realm.Ref),
		)
		if ResourceRequiresGuild(rule.Resource) {
			guildMembership, err := client.FetchGuildMembership(rule.GuildID)
			if err != nil {
				d.logger.Debug("failed to fetch guild membership, skipping rule",
					zap.String("user_id", identity.ID),
					zap.String("guild_id", rule.GuildID),
					zap.String("realm", realm.Ref),
					zap.Error(err),
				)
				continue
			}

			if rule.Resource == DiscordRoleRule {
				matchedRole := RoleChecker(rule.Identifier, guildMembership.Roles)

				if matchedRole != "" {
					// Authorised based on role whitelist.
					allowed = true
					break
				} else {
					d.logger.Debug("authenticated member does not have role",
						zap.String("member_id", identity.ID),
						zap.String("guild_id", rule.GuildID),
						zap.Strings("member_roles", guildMembership.Roles),
						zap.String("rule", rule.Identifier),
						zap.String("realm", realm.Ref),
					)
				}
			}

			if rule.Resource == DiscordGuildRule {
				if rule.Wildcard == true {
					// Authorised based on wildcard user within guild.
					allowed = true
					break
				}
			}

			if rule.Resource == DiscordMemberRule {
				if identity.ID == rule.Identifier {
					// Authorised based on user whitelist.
					allowed = true
					break
				}
			}
		} else if rule.Resource == DiscordUserRule && (rule.Wildcard || rule.Identifier == identity.ID) {
			allowed = true
			break
		}
	}

	// Re-validate user through OAuth2 flow every 16 hours when authorised
	expiration := time.Now().Add(time.Hour * 16)

	// Otherwise re-validate user every 3 minutes if authorised failed
	// in-case of Discord role change, etc.
	if !allowed {
		expiration = time.Now().Add(time.Minute * 3)
		d.logger.Info("access denied: user does not meet any authorization rules",
			zap.String("user_id", identity.ID),
			zap.String("username", identity.Username),
			zap.String("realm", realm.Ref),
		)
	}

	authedToken := NewAuthenticatedToken(*identity, realm.Ref, expiration, allowed)
	signedToken, err := d.tokenSigner(authedToken)
	if err != nil {
		d.logger.Error("failed to generate authenticated token",
			zap.String("user_id", identity.ID),
			zap.String("realm", realm.Ref),
			zap.Error(err),
		)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return err
	}

	cookie := &http.Cookie{
		Name:     d.cookie(realm.Ref),
		Value:    signedToken,
		Expires:  expiration,
		HttpOnly: true,
		// Strict mode breaks functionality - due to discord referrer.
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		//Secure // TODO: Configurable
	}

	redirectToURL, _ := url.Parse(token.RedirectURI)
	if r.Host != redirectToURL.Host {
		q := redirectToURL.Query()
		q.Set("DISCO_PASSTHROUGH", cookie.Value)
		q.Set("DISCO_REALM", realm.Ref)
		redirectToURL.RawQuery = q.Encode()
	} else {
		http.SetCookie(w, cookie)
	}

	http.Redirect(w, r, redirectToURL.String(), http.StatusFound)

	return nil
}

func RoleChecker(desiredRoleID string, roles []string) string {
	for _, role := range roles {
		if role == desiredRoleID {
			return role
		}
	}

	return ""
}
