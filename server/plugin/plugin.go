package plugin

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sync"

	"github.com/blang/semver"
	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/plugin"
	"github.com/matterpoll/matterpoll/server/poll"
	"github.com/matterpoll/matterpoll/server/store"
	"github.com/matterpoll/matterpoll/server/store/kvstore"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pkg/errors"
)

// MatterpollPlugin is the object to run the plugin
type MatterpollPlugin struct {
	plugin.MattermostPlugin
	botUserID string
	bundle    *i18n.Bundle
	router    *mux.Router
	Store     store.Store

	// activated is used to track whether or not OnActivate has initialized the plugin state.
	activated bool

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration
	ServerConfig  *model.Config
}

var botDescription = &i18n.Message{
	ID:    "bot.description",
	Other: "Poll Bot",
}

const (
	minimumServerVersion = "5.10.0"

	botUserName    = "matterpoll"
	botDisplayName = "Matterpoll"
)

// OnActivate ensures a configuration is set and initializes the API
func (p *MatterpollPlugin) OnActivate() error {
	var err error
	if err = p.checkServerVersion(); err != nil {
		return err
	}

	if p.ServerConfig.ServiceSettings.SiteURL == nil {
		return errors.New("siteURL is not set. Please set a siteURL and restart the plugin")
	}

	p.Store, err = kvstore.NewStore(p.API, manifest.Version)
	if err != nil {
		return errors.Wrap(err, "failed to create store")
	}

	p.bundle, err = p.initBundle()
	if err != nil {
		return errors.Wrap(err, "failed to init localisation bundle")
	}

	bot := &model.Bot{
		Username:    botUserName,
		DisplayName: botDisplayName,
	}
	botUserID, appErr := p.Helpers.EnsureBot(bot)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to ensure bot user")
	}
	p.botUserID = botUserID

	if err = p.patchBotDescription(); err != nil {
		return errors.Wrap(err, "failed to patch bot description")
	}

	if err = p.setProfileImage(); err != nil {
		return errors.Wrap(err, "failed to set profile image")
	}

	p.router = p.InitAPI()

	p.setActivated(true)

	return nil
}

// OnDeactivate marks the plugin as deactivated
func (p *MatterpollPlugin) OnDeactivate() error {
	p.setActivated(false)

	return nil
}

func (p *MatterpollPlugin) setActivated(activated bool) {
	p.activated = activated
}

func (p *MatterpollPlugin) isActivated() bool {
	return p.activated
}

// checkServerVersion checks Mattermost Server has at least the required version
func (p *MatterpollPlugin) checkServerVersion() error {
	serverVersion, err := semver.Parse(p.API.GetServerVersion())
	if err != nil {
		return errors.Wrap(err, "failed to parse server version")
	}

	r := semver.MustParseRange(">=" + minimumServerVersion)
	if !r(serverVersion) {
		return fmt.Errorf("this plugin requires Mattermost v%s or later", minimumServerVersion)
	}

	return nil
}

// patchBotDescription updates the bot description based on the servers local
func (p *MatterpollPlugin) patchBotDescription() error {
	publicLocalizer := p.getServerLocalizer()
	description := p.LocalizeDefaultMessage(publicLocalizer, botDescription)

	// Update description with server local
	botPatch := &model.BotPatch{
		Description: &description,
	}
	if _, appErr := p.API.PatchBot(p.botUserID, botPatch); appErr != nil {
		return errors.Wrap(appErr, "failed to patch bot")
	}

	return nil
}

// setProfileImage set the profile image of the bot account
func (p *MatterpollPlugin) setProfileImage() error {
	bundlePath, err := p.API.GetBundlePath()
	if err != nil {
		return errors.Wrap(err, "failed to get bundle path")
	}

	profileImage, err := ioutil.ReadFile(filepath.Join(bundlePath, "assets", "logo_dark.png"))
	if err != nil {
		return errors.Wrap(err, "failed to read profile image")
	}
	if appErr := p.API.SetProfileImage(p.botUserID, profileImage); appErr != nil {
		return errors.Wrap(appErr, "failed to set profile image")
	}
	return nil
}

// ConvertUserIDToDisplayName returns the display name to a given user ID
func (p *MatterpollPlugin) ConvertUserIDToDisplayName(userID string) (string, *model.AppError) {
	user, err := p.API.GetUser(userID)
	if err != nil {
		return "", err
	}
	displayName := user.GetDisplayName(model.SHOW_USERNAME)
	displayName = "@" + displayName
	return displayName, nil
}

// ConvertCreatorIDToDisplayName returns the display name to a given user ID of a poll creator
func (p *MatterpollPlugin) ConvertCreatorIDToDisplayName(creatorID string) (string, *model.AppError) {
	user, err := p.API.GetUser(creatorID)
	if err != nil {
		return "", err
	}
	displayName := user.GetDisplayName(model.SHOW_NICKNAME_FULLNAME)
	return displayName, nil
}

// HasPermission checks if a given user has the permission to end or delete a given poll
func (p *MatterpollPlugin) HasPermission(poll *poll.Poll, issuerID string) (bool, *model.AppError) {
	if issuerID == poll.Creator {
		return true, nil
	}

	user, appErr := p.API.GetUser(issuerID)
	if appErr != nil {
		return false, appErr
	}
	if user.IsInRole(model.SYSTEM_ADMIN_ROLE_ID) {
		return true, nil
	}
	return false, nil
}

// SendEphemeralPost sends an ephemeral post to a user as the bot account
func (p *MatterpollPlugin) SendEphemeralPost(channelID, userID, message string) {
	ephemeralPost := &model.Post{
		ChannelId: channelID,
		UserId:    p.botUserID,
		Message:   message,
	}
	_ = p.API.SendEphemeralPost(userID, ephemeralPost)
}
