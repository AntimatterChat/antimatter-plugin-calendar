// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pkg/errors"
	"golang.org/x/text/language"
)

// translationsDir is where the translations of the server's messages are, in the plugin bundle:
// active.<locale>.json files, as goi18n merge writes them. English is in the code.
const translationsDir = "assets/i18n"

// newTranslations returns a bundle with the English messages and the translations in a directory.
func newTranslations(dir string) (*i18n.Bundle, error) {
	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	files, err := os.ReadDir(dir)
	if err != nil {
		return bundle, errors.Wrap(err, "failed to read the translations")
	}
	for _, file := range files {
		name := file.Name()
		if !strings.HasPrefix(name, "active.") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if _, err := bundle.LoadMessageFile(filepath.Join(dir, name)); err != nil {
			return bundle, errors.Wrapf(err, "failed to load the translations in %s", name)
		}
	}
	return bundle, nil
}

// translator translates the server's messages to a user's language.
type translator struct {
	localizer *i18n.Localizer
}

// english is a bundle without translations.
var english = i18n.NewBundle(language.English)

// newTranslator returns the translator to a locale (a Mattermost locale such as "fr" or
// "pt-BR"), which falls back to English.
func newTranslator(bundle *i18n.Bundle, locale string) *translator {
	if bundle == nil {
		bundle = english
	}
	return &translator{localizer: i18n.NewLocalizer(bundle, locale, "en")}
}

// T translates a message. data fills its template; a Count field picks its plural form.
func (t *translator) T(message *i18n.Message, data map[string]any) string {
	config := &i18n.LocalizeConfig{DefaultMessage: message, TemplateData: data}
	if count, ok := data["Count"]; ok {
		config.PluralCount = count
	}
	text, err := t.localizer.Localize(config)
	if err != nil {
		// The English message, then
		text, _ = i18n.NewLocalizer(english).Localize(config)
	}
	return text
}
