// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';
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
        can_cancel: true,
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

        await waitFor(() => expect(mocked.scheduleCall).toHaveBeenCalledWith('channel1', expect.objectContaining({
            summary: 'Design review',
            recurrence_rule: '',
            reminder_minutes: 10,
            time_zone: expect.any(String),
        })));

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

    test('a weekly call on chosen days, ending after a number of calls, with its own reminder', async () => {
        mocked.scheduleCall.mockResolvedValue(testMeeting());
        renderDialog();
        act(() => openScheduleCallDialog({channelId: 'channel1'}));
        const dialog = await screen.findByRole('dialog', {name: 'Schedule a call'});

        fireEvent.change(screen.getByLabelText('Title'), {target: {value: 'Standup'}});
        fireEvent.change(screen.getByLabelText('Starts'), {target: {value: '2026-10-26'}}); // a Monday
        fireEvent.change(screen.getByLabelText('Repeat'), {target: {value: 'weekly'}});

        // The start's own day is picked; add Thursday.
        const days = screen.getByRole('group', {name: 'On'});
        expect(within(days).getByRole('button', {name: 'Monday'})).toHaveAttribute('aria-pressed', 'true');
        fireEvent.click(within(days).getByRole('button', {name: 'Thursday'}));

        fireEvent.change(screen.getByLabelText('Every'), {target: {value: '2'}});
        fireEvent.change(screen.getByLabelText('Stops'), {target: {value: 'after'}});
        fireEvent.change(screen.getByLabelText('Number of calls'), {target: {value: '6'}});
        fireEvent.change(screen.getByLabelText('Reminder'), {target: {value: '60'}});
        fireEvent.submit(dialog);

        await waitFor(() => expect(mocked.scheduleCall).toHaveBeenCalledWith('channel1', expect.objectContaining({
            recurrence_rule: 'FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH;COUNT=6',
            reminder_minutes: 60,
        })));
    });

    test('a daily call ending on a date, and the reminder choices', async () => {
        mocked.scheduleCall.mockResolvedValue(testMeeting());
        renderDialog();
        act(() => openScheduleCallDialog({channelId: 'channel1'}));
        const dialog = await screen.findByRole('dialog');

        const reminder = screen.getByLabelText('Reminder') as HTMLSelectElement;
        expect(reminder.value).toBe('10');
        expect(Array.from(reminder.options).map((o) => o.textContent)).toEqual([
            'None', 'When it starts', '5 minutes before', '10 minutes before', '15 minutes before', '30 minutes before', '1 hour before', 'A day before',
        ]);

        fireEvent.change(screen.getByLabelText('Title'), {target: {value: 'Standup'}});
        fireEvent.change(screen.getByLabelText('Starts'), {target: {value: '2026-10-26'}});
        fireEvent.change(screen.getByLabelText('Repeat'), {target: {value: 'daily'}});
        fireEvent.change(screen.getByLabelText('Stops'), {target: {value: 'on'}});
        fireEvent.change(screen.getByLabelText('Last day'), {target: {value: '2026-11-06'}});
        fireEvent.change(reminder, {target: {value: '-1'}});
        fireEvent.submit(dialog);

        await waitFor(() => expect(mocked.scheduleCall).toHaveBeenCalled());
        const input = mocked.scheduleCall.mock.calls[0][1];
        expect(input.recurrence_rule).toMatch(/^FREQ=DAILY;UNTIL=2026110\dT\d{6}Z$/);
        expect(input.reminder_minutes).toBe(-1);
    });

    test('refuses a weekly call with no day, and a last day before the first call', async () => {
        renderDialog();
        act(() => openScheduleCallDialog({channelId: 'channel1'}));
        const dialog = await screen.findByRole('dialog');

        fireEvent.change(screen.getByLabelText('Title'), {target: {value: 'Standup'}});
        fireEvent.change(screen.getByLabelText('Starts'), {target: {value: '2026-10-26'}});
        fireEvent.change(screen.getByLabelText('Repeat'), {target: {value: 'weekly'}});
        fireEvent.click(within(screen.getByRole('group', {name: 'On'})).getByRole('button', {name: 'Monday'}));
        fireEvent.submit(dialog);
        expect(await screen.findByText('Pick at least one day.')).toBeInTheDocument();

        fireEvent.click(within(screen.getByRole('group', {name: 'On'})).getByRole('button', {name: 'Monday'}));
        fireEvent.change(screen.getByLabelText('Stops'), {target: {value: 'on'}});
        fireEvent.change(screen.getByLabelText('Last day'), {target: {value: '2026-10-20'}});
        fireEvent.submit(dialog);
        expect(await screen.findByText('The last day can’t be before the first call.')).toBeInTheDocument();
        expect(mocked.scheduleCall).not.toHaveBeenCalled();
    });

    test('editing a series fills the picker in, and a rule it can\'t show is kept as it is', async () => {
        mocked.updateMeeting.mockResolvedValue(testMeeting());
        renderDialog();

        const series = testMeeting({recurrence_rule: 'FREQ=WEEKLY;BYDAY=MO,WE', reminder_minutes: 30});
        act(() => openScheduleCallDialog({channelId: series.channel_id, meeting: series}));
        let dialog = await screen.findByRole('dialog', {name: 'Edit the call'});
        expect(screen.getByLabelText('Repeat')).toHaveValue('weekly');
        expect(screen.getByLabelText('Reminder')).toHaveValue('30');
        const days = screen.getByRole('group', {name: 'On'});
        expect(within(days).getByRole('button', {name: 'Wednesday'})).toHaveAttribute('aria-pressed', 'true');
        fireEvent.submit(dialog);
        expect(mocked.updateMeeting).toHaveBeenCalledWith('meeting1', expect.objectContaining({recurrence_rule: 'FREQ=WEEKLY;BYDAY=MO,WE', reminder_minutes: 30}));
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());

        const custom = testMeeting({recurrence_rule: 'FREQ=MONTHLY;BYDAY=1MO'});
        act(() => openScheduleCallDialog({channelId: custom.channel_id, meeting: custom}));
        dialog = await screen.findByRole('dialog', {name: 'Edit the call'});
        expect(screen.getByLabelText('Repeat')).toHaveValue('custom');
        fireEvent.submit(dialog);
        expect(mocked.updateMeeting).toHaveBeenLastCalledWith('meeting1', expect.objectContaining({recurrence_rule: 'FREQ=MONTHLY;BYDAY=1MO'}));
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
