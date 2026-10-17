// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import * as client from '../client';
import type {EventDetails, Occurrence} from '../client';

import EventEditor from './event_editor';
import {FUSION_LAYER_ID} from './fusion/fusion_ui';

jest.mock('react-redux', () => ({useSelector: (selector: (state: object) => unknown) => selector({})}));
jest.mock('mattermost-redux/selectors/entities/channels', () => ({
    getMyChannels: () => [],
    getCurrentChannelId: () => '',
}));
jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    getEvent: jest.fn(),
    updateEvent: jest.fn(),
    deleteEvent: jest.fn(),
}));

const mocked = client as jest.Mocked<typeof client>;

const occurrence: Occurrence = {
    id: '/cal/work/sync.ics#2',
    calendar: '/cal/work/',
    path: '/cal/work/sync.ics',
    etag: 'e1',
    uid: 'sync',
    summary: 'Sync',
    all_day: false,
    start: Date.parse('2026-10-12T12:00:00Z'),
    end: Date.parse('2026-10-12T12:30:00Z'),
    recurrence: 'weekly',
    recurrence_id: Date.parse('2026-10-12T12:00:00Z'),
};

function details(start: string, end: string, extra: Partial<EventDetails> = {}): EventDetails {
    return {
        calendar: '/cal/work/',
        path: occurrence.path,
        etag: 'e1',
        summary: 'Sync',
        description: '',
        location: '',
        all_day: false,
        start,
        end,
        time_zone: 'UTC',
        recurrence: 'weekly',
        alarm: null,
        ...extra,
    };
}

function renderEditor(onDone = jest.fn()) {
    render(
        <IntlProvider locale='en'>
            <EventEditor
                calendars={[{id: '/cal/work/', name: 'Work', read_only: false}]}
                occurrence={occurrence}
                day={new Date()}
                onDone={onDone}
                onCancel={jest.fn()}
            />
        </IntlProvider>,
    );
    return onDone;
}

describe('EventEditor with a repeating event', () => {
    beforeEach(() => {
        mocked.getEvent.mockImplementation(async (_path, recurrenceID) => (recurrenceID ? details('2026-10-12T12:00:00Z', '2026-10-12T12:30:00Z', {recurrence_id: recurrenceID}) : details('2026-10-05T12:00:00Z', '2026-10-05T12:30:00Z')));
        mocked.updateEvent.mockResolvedValue({path: occurrence.path, etag: 'e2'});
        mocked.deleteEvent.mockResolvedValue({});
        jest.spyOn(window, 'confirm').mockReturnValue(true);
    });

    test('edits the occurrence alone by default', async () => {
        const onDone = renderEditor();
        const startDate = await screen.findByLabelText('Starts') as HTMLInputElement;
        expect(startDate.value).toBe('2026-10-12');
        expect(screen.getByRole('radio', {name: 'This event'})).toHaveAttribute('aria-checked', 'true');
        expect(screen.queryByLabelText('Repeat')).not.toBeInTheDocument();

        fireEvent.change(screen.getByLabelText('Title'), {target: {value: 'Sync (demo)'}});
        fireEvent.click(screen.getByText('Save'));
        await waitFor(() => expect(onDone).toHaveBeenCalled());
        expect(mocked.updateEvent).toHaveBeenCalledWith(occurrence.path, 'e1', expect.objectContaining({summary: 'Sync (demo)'}), occurrence.recurrence_id, 'this');
    });

    test('shows the series for all events, and deletes the following ones', async () => {
        renderEditor();
        await screen.findByLabelText('Starts');
        fireEvent.click(screen.getByRole('radio', {name: 'All events'}));
        expect((screen.getByLabelText('Starts') as HTMLInputElement).value).toBe('2026-10-05');
        expect(screen.getByLabelText('Repeat')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('radio', {name: 'This and following'}));
        expect((screen.getByLabelText('Starts') as HTMLInputElement).value).toBe('2026-10-12');
        fireEvent.click(screen.getByText('Delete'));
        await waitFor(() => expect(mocked.deleteEvent).toHaveBeenCalledWith(occurrence.path, 'e1', occurrence.recurrence_id, 'following'));
    });
});

describe('EventEditor under Fusion', () => {
    beforeEach(() => {
        window.antimatterWebUI = 'fusion';
        document.documentElement.dataset.amWebUi = 'fusion';
        const layer = document.createElement('div');
        layer.id = FUSION_LAYER_ID;
        layer.className = 'am-layer';
        document.body.appendChild(layer);
        mocked.getEvent.mockImplementation(async (_path, recurrenceID) => (recurrenceID ? details('2026-10-12T12:00:00Z', '2026-10-12T12:30:00Z', {recurrence_id: recurrenceID}) : details('2026-10-05T12:00:00Z', '2026-10-05T12:30:00Z')));
        mocked.deleteEvent.mockReset();
        mocked.deleteEvent.mockResolvedValue({});
    });

    afterEach(() => {
        delete window.antimatterWebUI;
        delete document.documentElement.dataset.amWebUi;
        document.getElementById(FUSION_LAYER_ID)?.remove();
        jest.restoreAllMocks();
    });

    test('asks with Fusion\'s dialog before deleting, and deletes only on yes', async () => {
        const native = jest.spyOn(window, 'confirm');
        native.mockClear();
        const onDone = renderEditor();
        await screen.findByLabelText('Starts');

        // No: nothing is deleted.
        fireEvent.click(screen.getByText('Delete'));
        const dialog = await screen.findByRole('alertdialog');
        expect(document.getElementById(FUSION_LAYER_ID)).toContainElement(dialog);
        expect(dialog).toHaveTextContent('Delete this event?');
        expect(dialog).toHaveTextContent('It’s deleted from your calendar server. This can’t be undone.');
        fireEvent.click(within(dialog).getByRole('button', {name: 'Cancel'}));
        await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
        expect(mocked.deleteEvent).not.toHaveBeenCalled();

        // Yes: the occurrence alone is deleted (the default scope).
        fireEvent.click(screen.getByText('Delete'));
        const again = await screen.findByRole('alertdialog');
        const yes = again.querySelector<HTMLButtonElement>('button[data-amfx-confirm="confirm"]')!;
        expect(yes).toHaveClass('am-btn', 'am-danger');
        fireEvent.click(yes);
        await waitFor(() => expect(mocked.deleteEvent).toHaveBeenCalledWith(occurrence.path, 'e1', occurrence.recurrence_id, 'this'));
        await waitFor(() => expect(onDone).toHaveBeenCalled());
        expect(native).not.toHaveBeenCalled();
    });

    test('the back button has Fusion\'s tooltip, not the browser\'s', async () => {
        renderEditor();
        await screen.findByLabelText('Starts');
        const back = screen.getAllByRole('button', {name: 'Cancel'}).find((b) => b.querySelector('svg'))!;
        expect(back).toHaveAttribute('data-am-tip', 'Cancel');
        expect(back).not.toHaveAttribute('title');
        expect(back.querySelector('use')).toHaveAttribute('href', '#am-i-chev');
    });
});
