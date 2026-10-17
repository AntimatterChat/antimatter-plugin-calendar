// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {fusionLayer} from './fusion_ui';

export type ConfirmOptions = {

    // The question, as the dialog's title ("Delete this note?").
    title: string;

    // What it means, under the title ("It's deleted for everyone. This can't be undone.").
    message?: string;

    // The confirming button ("Delete") and the other one ("Cancel"), already translated.
    confirmLabel: string;
    cancelLabel: string;

    // A destructive action: the confirming button is red.
    danger?: boolean;

    // An icon of Fusion's sprite for the confirming button (#am-i-<icon>), such as 'trash'.
    icon?: string;
};

// The dialog open, if any: opening another one answers it with a no.
let current: ((ok: boolean) => void) | null = null;

/**
 * confirmDialog asks before doing something, and resolves with whether the person agreed. Under the Fusion UI it's
 * Fusion's dialog (as its own confirmations: a title, a sentence, Cancel and the action), drawn in Fusion's layer;
 * Escape, Cancel or a click beside it says no, Enter or the action says yes. Under the classic UI, or a page without
 * Fusion's layer, it's the browser's confirmation, as before.
 *
 * It's plain DOM, not a React tree, so any code can await it (an event handler, an action) and it needs no provider.
 */
export function confirmDialog(options: ConfirmOptions): Promise<boolean> {
    const layer = fusionLayer();
    if (!layer) {
        const text = [options.title, options.message].filter(Boolean).join('\n\n');

        // The classic UI's confirmation, the one place an app asks with the browser's dialog.
        // eslint-disable-next-line no-alert
        return Promise.resolve(window.confirm(text));
    }

    current?.(false);
    return new Promise<boolean>((resolve) => {
        const returnFocus = document.activeElement as HTMLElement | null;
        const titleId = 'amfx-confirm-' + Math.random().toString(36).slice(2);

        const scrim = document.createElement('div');
        scrim.className = 'am-scrim';
        scrim.dataset.amfxConfirm = 'true';

        const form = document.createElement('form');
        form.className = 'am-modal am-small';
        form.setAttribute('role', 'alertdialog');
        form.setAttribute('aria-modal', 'true');
        form.setAttribute('aria-labelledby', titleId);
        scrim.appendChild(form);

        const pane = document.createElement('div');
        pane.className = 'am-pane';
        form.appendChild(pane);

        const title = document.createElement('h2');
        title.id = titleId;
        title.textContent = options.title;
        pane.appendChild(title);

        if (options.message) {
            const lead = document.createElement('p');
            lead.className = 'am-lead';
            lead.textContent = options.message;
            pane.appendChild(lead);
            form.setAttribute('aria-describedby', titleId + '-lead');
            lead.id = titleId + '-lead';
        }

        const actions = document.createElement('div');
        actions.className = 'am-modal-actions';
        pane.appendChild(actions);

        const cancel = document.createElement('button');
        cancel.type = 'button';
        cancel.className = 'am-btn';
        cancel.textContent = options.cancelLabel;
        cancel.dataset.amfxConfirm = 'cancel';
        actions.appendChild(cancel);

        const ok = document.createElement('button');
        ok.type = 'submit';
        ok.className = 'am-btn ' + (options.danger ? 'am-danger' : 'am-primary');
        ok.dataset.amfxConfirm = 'confirm';
        if (options.icon) {
            ok.appendChild(spriteIcon(options.icon));
        }
        ok.appendChild(document.createTextNode(options.confirmLabel));
        actions.appendChild(ok);

        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                // Only the dialog closes, not the panel or the dialog under it.
                e.preventDefault();
                e.stopPropagation();
                answer(false);
            }
        };

        const answer = (yes: boolean) => {
            if (current !== answer) {
                return;
            }
            current = null;
            document.removeEventListener('keydown', onKeyDown, true);
            scrim.remove();
            if (returnFocus?.isConnected) {
                returnFocus.focus?.();
            }
            resolve(yes);
        };

        cancel.addEventListener('click', () => answer(false));
        form.addEventListener('submit', (e) => {
            e.preventDefault();
            answer(true);
        });
        scrim.addEventListener('mousedown', (e) => {
            if (e.target === scrim) {
                answer(false);
            }
        });
        document.addEventListener('keydown', onKeyDown, true);

        current = answer;
        layer.appendChild(scrim);
        ok.focus();
    });
}

// spriteIcon draws an icon of Fusion's sprite, as Fusion's buttons hold them.
function spriteIcon(name: string): SVGSVGElement {
    const ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('class', 'am-ic am-sm');
    svg.setAttribute('aria-hidden', 'true');
    const use = document.createElementNS(ns, 'use');
    use.setAttribute('href', '#am-i-' + name);
    svg.appendChild(use);
    return svg;
}
