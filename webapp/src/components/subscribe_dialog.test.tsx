// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import * as client from '../client';

import SubscribeDialog from './subscribe_dialog';

jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    getFeed: jest.fn(),
    createFeed: jest.fn(),
}));

const mocked = client as jest.Mocked<typeof client>;

const FIRST = 'https://chat.example.com/plugins/com.antimatterchat.calendar/feeds/first.ics';
const SECOND = 'https://chat.example.com/plugins/com.antimatterchat.calendar/feeds/second.ics';
const WARNING = 'Anyone with this link can see these calls. Keep it private.';

const link = (url: string) => ({url, path: new URL(url).pathname, created_at: Date.UTC(2026, 9, 24)});

function setClipboard(writeText?: jest.Mock) {
    Object.defineProperty(navigator, 'clipboard', {value: writeText ? {writeText} : undefined, configurable: true});
}

function renderDialog(onClose = jest.fn()) {
    render(
        <IntlProvider locale='en'>
            <SubscribeDialog
                teamId='team1'
                onClose={onClose}
            />
        </IntlProvider>,
    );
    return onClose;
}

describe('SubscribeDialog', () => {
    afterEach(() => {
        setClipboard(undefined);
        jest.clearAllMocks();
    });

    test('makes the link when the person has none, and warns that it\'s private', async () => {
        mocked.getFeed.mockResolvedValue({exists: false});
        mocked.createFeed.mockResolvedValue(link(FIRST));
        renderDialog();

        expect(screen.getByRole('dialog', {name: 'Subscribe to community calls'})).toBeInTheDocument();
        expect(screen.getByTestId('feed-warning')).toHaveTextContent(WARNING);
        const field = await screen.findByLabelText('Calendar link');
        expect(field).toHaveValue(FIRST);
        expect(field).toHaveAttribute('readonly');
        expect(mocked.getFeed).toHaveBeenCalledWith('team1');
        expect(mocked.createFeed).toHaveBeenCalledTimes(1);
        expect(screen.getByText(WARNING)).toBeInTheDocument();
    });

    test('builds the address from the page when the server has no Site URL', async () => {
        mocked.getFeed.mockResolvedValue({exists: false});
        mocked.createFeed.mockResolvedValue({path: '/plugins/com.antimatterchat.calendar/feeds/first.ics', created_at: 1});
        renderDialog();

        expect(await screen.findByLabelText('Calendar link')).toHaveValue(`${window.location.origin}/plugins/com.antimatterchat.calendar/feeds/first.ics`);
    });

    test('Copy puts the link on the clipboard', async () => {
        const writeText = jest.fn().mockResolvedValue(undefined);
        setClipboard(writeText);
        mocked.getFeed.mockResolvedValue({exists: false});
        mocked.createFeed.mockResolvedValue(link(FIRST));
        renderDialog();

        await screen.findByLabelText('Calendar link');
        fireEvent.click(screen.getByRole('button', {name: 'Copy'}));
        await waitFor(() => expect(writeText).toHaveBeenCalledWith(FIRST));
        expect(await screen.findByRole('button', {name: 'Copied'})).toBeInTheDocument();
    });

    test.each([
        ['no clipboard', undefined],
        ['the clipboard refused', jest.fn().mockRejectedValue(new Error('denied'))],
    ])('Copy selects the link when it can\'t copy it (%s)', async (_, writeText) => {
        setClipboard(writeText);
        mocked.getFeed.mockResolvedValue({exists: false});
        mocked.createFeed.mockResolvedValue(link(FIRST));
        renderDialog();

        const field = await screen.findByLabelText('Calendar link') as HTMLInputElement;
        fireEvent.click(screen.getByRole('button', {name: 'Copy'}));
        expect(await screen.findByText('Press Ctrl+C (⌘C on a Mac) to copy the selected link.')).toBeInTheDocument();
        expect(document.activeElement).toBe(field);
        expect(field.selectionStart).toBe(0);
        expect(field.selectionEnd).toBe(FIRST.length);
        expect(screen.queryByRole('button', {name: 'Copied'})).not.toBeInTheDocument();
    });

    test('a link made before isn\'t shown again; Reset link asks in the dialog, then makes a new one', async () => {
        mocked.getFeed.mockResolvedValue({exists: true, created_at: Date.UTC(2026, 9, 20, 12)});
        mocked.createFeed.mockResolvedValue(link(SECOND));
        renderDialog();

        expect(await screen.findByText(/You made a link on Oct 20, 2026/)).toBeInTheDocument();
        expect(screen.queryByLabelText('Calendar link')).not.toBeInTheDocument();
        expect(mocked.createFeed).not.toHaveBeenCalled();
        expect(screen.getByText(WARNING)).toBeInTheDocument();

        // The confirmation is in the dialog itself; Cancel leaves everything as it was.
        fireEvent.click(screen.getByRole('button', {name: 'Reset link'}));
        expect(screen.getByRole('alert')).toHaveTextContent('The current link stops working at once');
        expect(mocked.createFeed).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        expect(mocked.createFeed).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Reset link'}));
        fireEvent.click(screen.getByRole('button', {name: 'Reset link'}));
        expect(await screen.findByLabelText('Calendar link')).toHaveValue(SECOND);
        expect(mocked.createFeed).toHaveBeenCalledTimes(1);
        expect(mocked.createFeed).toHaveBeenCalledWith('team1');
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        expect(screen.getByText(WARNING)).toBeInTheDocument();
    });

    test('resetting a shown link replaces it', async () => {
        mocked.getFeed.mockResolvedValue({exists: false});
        mocked.createFeed.mockResolvedValueOnce(link(FIRST)).mockResolvedValueOnce(link(SECOND));
        renderDialog();

        expect(await screen.findByLabelText('Calendar link')).toHaveValue(FIRST);
        fireEvent.click(screen.getByRole('button', {name: 'Reset link'}));
        fireEvent.click(screen.getByRole('button', {name: 'Reset link'}));
        await waitFor(() => expect(screen.getByLabelText('Calendar link')).toHaveValue(SECOND));
    });

    test('Escape closes the confirmation first, then the dialog', async () => {
        mocked.getFeed.mockResolvedValue({exists: true, created_at: 1});
        const onClose = renderDialog();
        await screen.findByText(/You made a link on/);

        fireEvent.click(screen.getByRole('button', {name: 'Reset link'}));
        fireEvent.keyDown(document, {key: 'Escape'});
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        expect(onClose).not.toHaveBeenCalled();

        fireEvent.keyDown(document, {key: 'Escape'});
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    test('says why it failed', async () => {
        mocked.getFeed.mockRejectedValue(new Error('calendar feeds are turned off'));
        renderDialog();
        expect(await screen.findByText('calendar feeds are turned off')).toBeInTheDocument();
    });
});
