// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {isFusionUI} from './web_ui';

type ClassArg = string | false | null | undefined | {[name: string]: boolean | undefined};

function names(args: ClassArg[]): string[] {
    const result: string[] = [];
    for (const arg of args) {
        if (!arg) {
            continue;
        }
        if (typeof arg === 'string') {
            result.push(arg);
            continue;
        }
        for (const [name, on] of Object.entries(arg)) {
            if (on) {
                result.push(name);
            }
        }
    }
    return result;
}

// am returns the class names of the calendar panel's building blocks, which are those of the Fusion
// mockup: Fusion styles them (am-app-row...), and the plugin's own stylesheet styles the same
// blocks for the classic web app (amc-app-row...).
export function am(...args: ClassArg[]): string {
    const prefix = isFusionUI() ? 'am-' : 'amc-';
    return names(args).map((name) => prefix + name).join(' ');
}

// own returns the class names of the parts of the calendar panel the Fusion mockup doesn't have, which
// the plugin styles for both web apps.
export function own(...args: ClassArg[]): string {
    return names(args).map((name) => 'amc-' + name).join(' ');
}
