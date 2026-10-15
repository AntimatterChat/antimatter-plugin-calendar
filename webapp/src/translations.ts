// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

// Generated from i18n/*.json by the i18n tooling: run it again when a locale is added. The English is each
// message's defaultMessage, and i18n/en.json is extracted from the code (formatjs extract).

import ar from '../i18n/ar.json';
import bg from '../i18n/bg.json';
import bn from '../i18n/bn.json';
import ca from '../i18n/ca.json';
import cs from '../i18n/cs.json';
import da from '../i18n/da.json';
import de from '../i18n/de.json';
import el from '../i18n/el.json';
import es from '../i18n/es.json';
import fa from '../i18n/fa.json';
import fi from '../i18n/fi.json';
import fil from '../i18n/fil.json';
import fr from '../i18n/fr.json';
import he from '../i18n/he.json';
import hi from '../i18n/hi.json';
import hu from '../i18n/hu.json';
import id from '../i18n/id.json';
import it from '../i18n/it.json';
import ja from '../i18n/ja.json';
import ko from '../i18n/ko.json';
import ms from '../i18n/ms.json';
import nbNO from '../i18n/nb-NO.json';
import nl from '../i18n/nl.json';
import pl from '../i18n/pl.json';
import pt from '../i18n/pt.json';
import ptBR from '../i18n/pt-BR.json';
import ro from '../i18n/ro.json';
import ru from '../i18n/ru.json';
import sv from '../i18n/sv.json';
import ta from '../i18n/ta.json';
import th from '../i18n/th.json';
import tr from '../i18n/tr.json';
import uk from '../i18n/uk.json';
import ur from '../i18n/ur.json';
import vi from '../i18n/vi.json';
import zhCN from '../i18n/zh-CN.json';
import zhTW from '../i18n/zh-TW.json';

const catalogs: Record<string, Record<string, string>> = {
    ar,
    bg,
    bn,
    ca,
    cs,
    da,
    de,
    el,
    es,
    fa,
    fi,
    fil,
    fr,
    he,
    hi,
    hu,
    id,
    it,
    ja,
    ko,
    ms,
    'nb-NO': nbNO,
    nl,
    pl,
    pt,
    'pt-BR': ptBR,
    ro,
    ru,
    sv,
    ta,
    th,
    tr,
    uk,
    ur,
    vi,
    'zh-CN': zhCN,
    'zh-TW': zhTW,
};

// translationsFor gives the strings of a locale (e.g. pt-BR), or of its language (pt) when it has none of its own.
export function translationsFor(locale: string): Record<string, string> {
    return catalogs[locale] || catalogs[locale.split('-')[0]] || {};
}

type TranslationsRegistry = {
    registerTranslations?: (getTranslationsForLocale: (locale: string) => Record<string, string>) => void;
};

// registerTranslations gives the app's strings to the host, which shows them in the user's language.
export function registerTranslations(registry: unknown) {
    (registry as TranslationsRegistry).registerTranslations?.(translationsFor);
}
