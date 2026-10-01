// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import * as client from '../client';
import type {EventDetails, Occurrence} from '../client';

import EventEditor from './event_editor';

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
