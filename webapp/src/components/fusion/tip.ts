// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {isFusion} from './fusion_ui';

export type TipProps = {'data-am-tip': string} | {title: string};

// tip gives an element its tooltip, spread on it (<button {...tip(label)}>): Fusion's tooltip under the Fusion UI (a
// data-am-tip attribute, which Fusion shows above the element under the pointer or the keyboard, in its own style),
// the browser's (a title) under the classic UI.
export function tip(label: string): TipProps {
    return isFusion() ? {'data-am-tip': label} : {title: label};
}

// iconTip is tip for a control that shows no text (an icon button): the label also names it for screen readers, as
// Fusion's tooltip is drawn apart from the element.
export function iconTip(label: string): TipProps & {'aria-label': string} {
    return {...tip(label), 'aria-label': label};
}
