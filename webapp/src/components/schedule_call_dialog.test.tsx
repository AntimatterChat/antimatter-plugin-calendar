// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import * as client from '../client';
import type {Meeting} from '../client';
import {openScheduleCallDialog} from '../schedule_call';

import ScheduleCallDialogRoot from './schedule_call_dialog';

jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    scheduleCall: jest.fn(),
    updateMeeting: jest.fn(),
}));

const mocked = client as jest.Mocked<typeof client>;

function testMeeting(overrides: Partial<Meeting> = {}): Meeting {
    return {
        id: 'meeting1',
        channel_id: 'channel1',
        post_id: 'post1',
        organizer_id: 'organizer1',
        summary: 'Team sync',
        description: 'Weekly catch-up',
        start: new Date(2026, 9, 27, 14, 0).getTime(),
        end: new Date(2026, 9, 27, 14, 30).getTime(),
        sequence: 0,
        cancelled: false,
        created_at: 0,
        updated_at: 0,
        ...overrides,
    };
}

afterEach(() => jest.clearAllMocks());

function renderDialog() {
    return render(
        <IntlProvider locale='en'>
            <ScheduleCallDialogRoot/>
        </IntlProvider>,
    );
}

describe('ScheduleCallDialogRoot', () => {
    test('renders nothing until opened', () => {
        renderDialog();
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    test('schedules a new call for the channel it was opened for', async () => {
        mocked.scheduleCall.mockResolvedValue(testMeeting());
        renderDialog();

        act(() => openScheduleCallDialog({channelId: 'channel1'}));
        const dialog = await screen.findByRole('dialog', {name: 'Schedule a call'});

        fireEvent.change(screen.getByLabelText('Title'), {target: {value: 'Design review'}});
        fireEvent.submit(dialog);

        await waitFor(() => expect(mocked.scheduleCall).toHaveBeenCalledWith('channel1', expect.objectContaining({summary: 'Design review'})));

        // The dialog closes once scheduled.
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    });

    test('editing a meeting pre-fills its fields and calls updateMeeting, not scheduleCall', async () => {
        mocked.updateMeeting.mockResolvedValue(testMeeting());
        renderDialog();

        const meeting = testMeeting();
        act(() => openScheduleCallDialog({channelId: meeting.channel_id, meeting}));
        const dialog = await screen.findByRole('dialog', {name: 'Edit the call'});

        expect(screen.getByLabelText('Title')).toHaveValue('Team sync');
        expect(screen.getByLabelText('Description')).toHaveValue('Weekly catch-up');

        fireEvent.submit(dialog);
        expect(mocked.updateMeeting).toHaveBeenCalledWith('meeting1', expect.objectContaining({summary: 'Team sync'}));
        expect(mocked.scheduleCall).not.toHaveBeenCalled();
    });

    test('refuses a call that ends before (or when) it starts', async () => {
        renderDialog();
        act(() => openScheduleCallDialog({channelId: 'channel1'}));
        const dialog = await screen.findByRole('dialog');

        fireEvent.change(screen.getByLabelText('Title'), {target: {value: 'Standup'}});
        const startDate = (screen.getByLabelText('Starts') as HTMLInputElement).value;
        fireEvent.change(screen.getByLabelText('Ends'), {target: {value: startDate}});
        fireEvent.change(document.getElementById('amc-call-end-time') as HTMLInputElement, {target: {value: '00:00'}});
        fireEvent.submit(dialog);

        expect(await screen.findByText('The call must end after it starts.')).toBeInTheDocument();
        expect(mocked.scheduleCall).not.toHaveBeenCalled();
    });

    test('Escape, and a click outside, both close it without saving', async () => {
        renderDialog();
        act(() => openScheduleCallDialog({channelId: 'channel1'}));
        await screen.findByRole('dialog');

        fireEvent.keyDown(document, {key: 'Escape'});
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

        act(() => openScheduleCallDialog({channelId: 'channel1'}));
        const scrim = (await screen.findByRole('dialog')).parentElement as HTMLElement;
        fireEvent.mouseDown(scrim);
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        expect(mocked.scheduleCall).not.toHaveBeenCalled();
    });
});
