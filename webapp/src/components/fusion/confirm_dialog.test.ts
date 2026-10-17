// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {confirmDialog} from './confirm_dialog';
import {FUSION_LAYER_ID} from './fusion_ui';

const setWebUI = (ui?: 'classic' | 'fusion') => {
    window.antimatterWebUI = ui;
    if (ui) {
        document.documentElement.dataset.amWebUi = ui;
    } else {
        delete document.documentElement.dataset.amWebUi;
    }
};

const options = {
    title: 'Delete this note?',
    message: 'It\'s deleted for everyone.',
    confirmLabel: 'Delete',
    cancelLabel: 'Cancel',
    danger: true,
    icon: 'trash',
};

const dialog = () => document.querySelector<HTMLElement>('[role="alertdialog"]');
const button = (which: 'cancel' | 'confirm') => document.querySelector<HTMLButtonElement>(`button[data-amfx-confirm="${which}"]`)!;

describe('confirmDialog', () => {
    let confirmSpy: jest.SpyInstance;

    beforeEach(() => {
        document.body.innerHTML = `<div id="${FUSION_LAYER_ID}" class="am-layer"></div><button id="opener">open</button>`;
        confirmSpy = jest.spyOn(window, 'confirm').mockReturnValue(true);
    });

    afterEach(() => {
        confirmSpy.mockRestore();
        setWebUI(undefined);
    });

    describe('under Fusion', () => {
        beforeEach(() => setWebUI('fusion'));

        test('draws Fusion\'s dialog in its layer, not the browser\'s', async () => {
            const answer = confirmDialog(options);

            const layer = document.getElementById(FUSION_LAYER_ID)!;
            const el = dialog()!;
            expect(layer.contains(el)).toBe(true);
            expect(el.closest('.am-scrim')).not.toBeNull();
            expect(el.className).toBe('am-modal am-small');
            expect(el.getAttribute('aria-modal')).toBe('true');
            expect(el.querySelector('.am-pane h2')!.textContent).toBe('Delete this note?');
            expect(el.querySelector('.am-pane p.am-lead')!.textContent).toBe('It\'s deleted for everyone.');
            expect(el.getAttribute('aria-labelledby')).toBe(el.querySelector('h2')!.id);
            expect(button('cancel').className).toBe('am-btn');
            expect(button('cancel').textContent).toBe('Cancel');
            expect(button('confirm').className).toBe('am-btn am-danger');
            expect(button('confirm').textContent).toBe('Delete');
            expect(button('confirm').querySelector('use')!.getAttribute('href')).toBe('#am-i-trash');
            expect(document.activeElement).toBe(button('confirm'));
            expect(confirmSpy).not.toHaveBeenCalled();

            button('confirm').click();
            await expect(answer).resolves.toBe(true);
            expect(dialog()).toBeNull();
        });

        test('Cancel says no', async () => {
            const answer = confirmDialog(options);
            button('cancel').click();
            await expect(answer).resolves.toBe(false);
            expect(dialog()).toBeNull();
        });

        test('Escape says no, and doesn\'t reach what\'s under the dialog', async () => {
            const under = jest.fn();
            document.addEventListener('keydown', under);
            const answer = confirmDialog(options);
            document.body.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
            await expect(answer).resolves.toBe(false);
            expect(under).not.toHaveBeenCalled();
            document.removeEventListener('keydown', under);
        });

        test('a click beside the dialog says no, a click inside it doesn\'t', async () => {
            const answer = confirmDialog(options);
            dialog()!.dispatchEvent(new MouseEvent('mousedown', {bubbles: true}));
            expect(dialog()).not.toBeNull();
            document.querySelector('.am-scrim')!.dispatchEvent(new MouseEvent('mousedown', {bubbles: true}));
            await expect(answer).resolves.toBe(false);
        });

        test('Enter (submitting the form) says yes', async () => {
            const answer = confirmDialog(options);
            dialog()!.dispatchEvent(new Event('submit', {bubbles: true, cancelable: true}));
            await expect(answer).resolves.toBe(true);
        });

        test('an action that isn\'t destructive gets the primary button, and no lead without a message', () => {
            confirmDialog({title: 'Disconnect?', confirmLabel: 'Disconnect', cancelLabel: 'Cancel'});
            expect(button('confirm').className).toBe('am-btn am-primary');
            expect(button('confirm').querySelector('svg')).toBeNull();
            expect(dialog()!.querySelector('.am-lead')).toBeNull();
        });

        test('gives the focus back to what had it', async () => {
            const opener = document.getElementById('opener')!;
            opener.focus();
            const answer = confirmDialog(options);
            expect(document.activeElement).not.toBe(opener);
            button('cancel').click();
            await answer;
            expect(document.activeElement).toBe(opener);
        });

        test('opening a second dialog answers the first with a no', async () => {
            const first = confirmDialog(options);
            const second = confirmDialog({...options, title: 'Second?'});
            await expect(first).resolves.toBe(false);
            expect(document.querySelectorAll('[role="alertdialog"]')).toHaveLength(1);
            expect(dialog()!.querySelector('h2')!.textContent).toBe('Second?');
            button('confirm').click();
            await expect(second).resolves.toBe(true);
        });

        test('the text is text, never markup', () => {
            confirmDialog({...options, title: '<img src=x onerror=alert(1)>'});
            expect(dialog()!.querySelector('img')).toBeNull();
            expect(dialog()!.querySelector('h2')!.textContent).toBe('<img src=x onerror=alert(1)>');
        });

        test('without Fusion\'s layer, falls back to the browser\'s confirmation', async () => {
            document.body.innerHTML = '';
            confirmSpy.mockReturnValue(false);
            await expect(confirmDialog(options)).resolves.toBe(false);
            expect(confirmSpy).toHaveBeenCalledWith('Delete this note?\n\nIt\'s deleted for everyone.');
        });
    });

    describe('under the classic UI', () => {
        beforeEach(() => setWebUI('classic'));

        test('keeps the browser\'s confirmation, with the title and the message', async () => {
            await expect(confirmDialog(options)).resolves.toBe(true);
            expect(confirmSpy).toHaveBeenCalledWith('Delete this note?\n\nIt\'s deleted for everyone.');
            expect(dialog()).toBeNull();

            confirmSpy.mockReturnValue(false);
            await expect(confirmDialog({...options, message: undefined})).resolves.toBe(false);
            expect(confirmSpy).toHaveBeenLastCalledWith('Delete this note?');
        });
    });
});
