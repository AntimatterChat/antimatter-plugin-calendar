// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

// The app's source, but its tests and the shared confirmation itself (the one place allowed to fall back to the
// browser's confirmation, under the classic UI).
const SRC = path.resolve(__dirname, '..', '..');
const ALLOWED = path.join(__dirname, 'confirm_dialog.ts');

function sourceFiles(dir: string): string[] {
    return fs.readdirSync(dir, {withFileTypes: true}).flatMap((entry) => {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) {
            return sourceFiles(full);
        }
        return (/\.(ts|tsx|js|jsx)$/).test(entry.name) && !(/\.test\.(ts|tsx|js|jsx)$/).test(entry.name) ? [full] : [];
    });
}

// window.confirm(...), globalThis.confirm(...), self.confirm(...) or a bare confirm(...).
const NATIVE_CONFIRM = /(?:\b(?:window|globalThis|self)\s*\.\s*confirm|(?:^|[^.\w$])confirm)\s*\(/;

describe('confirmations', () => {
    test('ask with Fusion\'s dialog (components/fusion/confirm_dialog), never the browser\'s', () => {
        const files = sourceFiles(SRC).filter((file) => file !== ALLOWED);
        expect(files.length).toBeGreaterThan(0);
        const offenders = files.flatMap((file) => fs.readFileSync(file, 'utf8').split('\n').
            map((line, i) => (NATIVE_CONFIRM.test(line) ? `${path.relative(SRC, file)}:${i + 1}: ${line.trim()}` : '')).
            filter(Boolean));
        expect(offenders).toEqual([]);
    });

    test('the check finds the browser\'s confirmation', () => {
        expect(NATIVE_CONFIRM.test('if (window.confirm(text)) {')).toBe(true);
        expect(NATIVE_CONFIRM.test('if (!confirm(text)) {')).toBe(true);
        expect(NATIVE_CONFIRM.test('globalThis.confirm(text)')).toBe(true);
        expect(NATIVE_CONFIRM.test('if (await confirmDialog({title})) {')).toBe(false);
        expect(NATIVE_CONFIRM.test('props.onConfirm()')).toBe(false);
    });
});
