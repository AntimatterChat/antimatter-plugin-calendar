// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';

import {getCurrentChannelId, getMyChannels} from 'mattermost-redux/selectors/entities/channels';

import {am, own} from '../class_names';
import * as client from '../client';
import type {Calendar, Occurrence, Recurrence} from '../client';
import {dateInput, parseLocal} from '../utils';

import {detailsForm, newEventForm, toInput, type Form} from './event_form';
import Icon from './icon';
import {messages} from './messages';

const ALARMS = [0, 5, 10, 15, 30, 60, 120, 1440];

type Props = {
    calendars: Calendar[];

    // The occurrence to edit, null for a new event on the given day
    occurrence: Occurrence | null;
    day: Date;
    onDone: () => void;
    onCancel: () => void;
};

function channelLabel(channel: {display_name: string; name: string; type: string}) {
    const name = channel.display_name || channel.name;
    return channel.type === 'O' || channel.type === 'P' ? `~${name}` : name;
}

// EventEditor creates, edits and deletes events. Repeating events are edited as a whole; one
// occurrence can be deleted.
export default function EventEditor({calendars, occurrence, day, onDone, onCancel}: Props) {
    const {formatMessage} = useIntl();
    const myChannels = useSelector(getMyChannels);
    const currentChannelId = useSelector(getCurrentChannelId);
    const channels = useMemo(() => myChannels.
        filter((c) => c.delete_at === 0).
        sort((a, b) => channelLabel(a).localeCompare(channelLabel(b))), [myChannels]);

    const [form, setForm] = useState<Form | null>(() => (occurrence ? null : newEventForm(calendars, day, currentChannelId)));
    const [etag, setETag] = useState('');
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');

    useEffect(() => {
        if (!occurrence) {
            return;
        }
        client.getEvent(occurrence.path).then((details) => {
            setForm(detailsForm(details, currentChannelId));
            setETag(details.etag);
        }).catch((err) => setError((err as Error).message));
    }, [occurrence?.path]);

    const calendar = calendars.find((c) => c.id === (form?.calendar || occurrence?.calendar));
    const readOnly = Boolean(calendar?.read_only);
    const repeating = Boolean(occurrence?.recurrence && occurrence.recurrence_id);

    const set = <K extends keyof Form>(key: K, value: Form[K]) => setForm((f) => (f ? {...f, [key]: value} : f));

    const run = async (action: () => Promise<unknown>) => {
        setSaving(true);
        setError('');
        try {
            await action();
            onDone();
        } catch (err) {
            setError((err as Error).message);
            setSaving(false);
        }
    };

    const save = (e: React.FormEvent) => {
        e.preventDefault();
        if (!form) {
            return;
        }
        const input = toInput(form);
        run(() => (occurrence ? client.updateEvent(occurrence.path, etag, input) : client.createEvent(input)));
    };

    const remove = (only: boolean) => {
        if (!occurrence) {
            return;
        }
        const confirmation = repeating && !only ? messages.deleteSeriesConfirm : messages.deleteConfirm;

        // eslint-disable-next-line no-alert
        if (!window.confirm(formatMessage(confirmation))) {
            return;
        }
        run(() => client.deleteEvent(occurrence.path, etag, only ? occurrence.recurrence_id : 0));
    };

    if (!form) {
        return (
            <div className={am('app-panel')}>
                {error ? <div className={own('error')}>{error}</div> : <div className={am('empty')}>{formatMessage(messages.loading)}</div>}
                <div className={own('actions')}>
                    <button
                        className={am('btn')}
                        onClick={onCancel}
                    >
                        {formatMessage(messages.cancel)}
                    </button>
                </div>
            </div>
        );
    }

    const alarmLabel = (minutes: number) => {
        if (minutes === 0) {
            return formatMessage(messages.alarmAtStart);
        }
        if (minutes === 1440) {
            return formatMessage(messages.alarmDay);
        }
        if (minutes >= 60) {
            return formatMessage(messages.alarmHours, {count: minutes / 60});
        }
        return formatMessage(messages.alarmMinutes, {count: minutes});
    };
    const alarms = form.alarm === null || ALARMS.includes(form.alarm) ? ALARMS : [...ALARMS, form.alarm].sort((a, b) => a - b);

    return (
        <form
            className={am('app-panel')}
            onSubmit={save}
        >
            <div className={own('toolbar')}>
                <button
                    type='button'
                    className={am('icon-btn')}
                    title={formatMessage(messages.cancel)}
                    aria-label={formatMessage(messages.cancel)}
                    onClick={onCancel}
                >
                    <Icon name='back'/>
                </button>
                <h3 className={own('editor-title')}>{formatMessage(occurrence ? messages.editTitle : messages.newTitle)}</h3>
            </div>
            {readOnly && <div className={own('notice')}>{formatMessage(messages.readOnly)}</div>}
            {repeating && !readOnly && <div className={own('notice')}>{formatMessage(messages.seriesNote)}</div>}
            <fieldset
                className={own('fieldset')}
                disabled={readOnly || saving}
            >
                <label
                    className={am('field')}
                    htmlFor='amc-summary'
                >
                    <span>{formatMessage(messages.summary)}</span>
                    <input
                        id='amc-summary'
                        type='text'
                        required={true}
                        maxLength={1000}
                        autoFocus={!occurrence}
                        value={form.summary}
                        onChange={(e) => set('summary', e.target.value)}
                    />
                </label>
                {!occurrence && (
                    <label
                        className={am('field')}
                        htmlFor='amc-calendar'
                    >
                        <span>{formatMessage(messages.calendar)}</span>
                        <select
                            id='amc-calendar'
                            value={form.calendar}
                            required={true}
                            onChange={(e) => set('calendar', e.target.value)}
                        >
                            {calendars.filter((c) => !c.read_only).map((c) => (
                                <option
                                    key={c.id}
                                    value={c.id}
                                >
                                    {c.name}
                                </option>
                            ))}
                        </select>
                    </label>
                )}
                <label className={own('check')}>
                    <input
                        type='checkbox'
                        checked={form.allDay}
                        onChange={(e) => set('allDay', e.target.checked)}
                    />
                    {formatMessage(messages.allDayEvent)}
                </label>
                <div className={own('field-row')}>
                    <label
                        className={am('field')}
                        htmlFor='amc-start-date'
                    >
                        <span>{formatMessage(messages.starts)}</span>
                        <input
                            id='amc-start-date'
                            type='date'
                            className={own('input')}
                            required={true}
                            value={form.startDate}
                            onChange={(e) => {
                                const startDate = e.target.value;

                                // Moving the start moves the end along
                                const shift = parseLocal(startDate).getTime() - parseLocal(form.startDate).getTime();
                                const endDate = dateInput(new Date(parseLocal(form.endDate).getTime() + shift));
                                setForm({...form, startDate, endDate: startDate && form.endDate ? endDate : startDate});
                            }}
                        />
                    </label>
                    {!form.allDay && (
                        <label
                            className={am('field')}
                            htmlFor='amc-start-time'
                        >
                            <span>{' '}</span>
                            <input
                                id='amc-start-time'
                                type='time'
                                className={own('input')}
                                required={true}
                                value={form.startTime}
                                onChange={(e) => set('startTime', e.target.value)}
                            />
                        </label>
                    )}
                </div>
                <div className={own('field-row')}>
                    <label
                        className={am('field')}
                        htmlFor='amc-end-date'
                    >
                        <span>{formatMessage(messages.ends)}</span>
                        <input
                            id='amc-end-date'
                            type='date'
                            className={own('input')}
                            required={true}
                            min={form.startDate}
                            value={form.endDate}
                            onChange={(e) => set('endDate', e.target.value)}
                        />
                    </label>
                    {!form.allDay && (
                        <label
                            className={am('field')}
                            htmlFor='amc-end-time'
                        >
                            <span>{' '}</span>
                            <input
                                id='amc-end-time'
                                type='time'
                                className={own('input')}
                                required={true}
                                value={form.endTime}
                                onChange={(e) => set('endTime', e.target.value)}
                            />
                        </label>
                    )}
                </div>
                <div className={own('field-row')}>
                    <label
                        className={am('field')}
                        htmlFor='amc-repeat'
                    >
                        <span>{formatMessage(messages.repeat)}</span>
                        <select
                            id='amc-repeat'
                            value={form.recurrence}
                            onChange={(e) => set('recurrence', e.target.value as Recurrence)}
                        >
                            <option value=''>{formatMessage(messages.repeatNone)}</option>
                            <option value='daily'>{formatMessage(messages.repeatDaily)}</option>
                            <option value='weekly'>{formatMessage(messages.repeatWeekly)}</option>
                            <option value='monthly'>{formatMessage(messages.repeatMonthly)}</option>
                            <option value='yearly'>{formatMessage(messages.repeatYearly)}</option>
                            {form.recurrence === 'custom' && <option value='custom'>{formatMessage(messages.repeatCustom)}</option>}
                        </select>
                    </label>
                    <label
                        className={am('field')}
                        htmlFor='amc-alarm'
                    >
                        <span>{formatMessage(messages.alarm)}</span>
                        <select
                            id='amc-alarm'
                            value={form.alarm === null ? '' : String(form.alarm)}
                            onChange={(e) => set('alarm', e.target.value === '' ? null : Number(e.target.value))}
                        >
                            <option value=''>{formatMessage(messages.alarmNone)}</option>
                            {alarms.map((minutes) => (
                                <option
                                    key={minutes}
                                    value={minutes}
                                >
                                    {alarmLabel(minutes)}
                                </option>
                            ))}
                        </select>
                    </label>
                </div>
                <div className={own('field-row')}>
                    <label
                        className={am('field')}
                        htmlFor='amc-link'
                    >
                        <span>{formatMessage(messages.link)}</span>
                        <select
                            id='amc-link'
                            value={form.linkKind}
                            onChange={(e) => set('linkKind', e.target.value as Form['linkKind'])}
                        >
                            <option value=''>{formatMessage(messages.linkNone)}</option>
                            <option value='channel'>{formatMessage(messages.linkChannel)}</option>
                            <option value='call'>{formatMessage(messages.linkCall)}</option>
                        </select>
                    </label>
                    {form.linkKind && (
                        <label
                            className={am('field')}
                            htmlFor='amc-channel'
                        >
                            <span>{formatMessage(messages.channel)}</span>
                            <select
                                id='amc-channel'
                                value={form.channelId}
                                required={true}
                                onChange={(e) => set('channelId', e.target.value)}
                            >
                                {!channels.some((c) => c.id === form.channelId) && <option value={form.channelId}>{form.channelId}</option>}
                                {channels.map((c) => (
                                    <option
                                        key={c.id}
                                        value={c.id}
                                    >
                                        {channelLabel(c)}
                                    </option>
                                ))}
                            </select>
                        </label>
                    )}
                </div>
                <label
                    className={am('field')}
                    htmlFor='amc-location'
                >
                    <span>{formatMessage(messages.location)}</span>
                    <input
                        id='amc-location'
                        type='text'
                        maxLength={1000}
                        value={form.location}
                        onChange={(e) => set('location', e.target.value)}
                    />
                </label>
                <label
                    className={am('field')}
                    htmlFor='amc-description'
                >
                    <span>{formatMessage(messages.description)}</span>
                    <textarea
                        id='amc-description'
                        value={form.description}
                        maxLength={20000}
                        onChange={(e) => set('description', e.target.value)}
                    />
                </label>
            </fieldset>
            {error && <div className={own('error')}>{error}</div>}
            {!readOnly && (
                <div className={own('actions')}>
                    <button
                        type='submit'
                        className={am('btn', 'primary')}
                        disabled={saving}
                    >
                        {formatMessage(saving ? messages.saving : messages.save)}
                    </button>
                    <button
                        type='button'
                        className={am('btn')}
                        onClick={onCancel}
                    >
                        {formatMessage(messages.cancel)}
                    </button>
                    <span className={am('grow')}/>
                    {occurrence && repeating && (
                        <button
                            type='button'
                            className={am('btn')}
                            disabled={saving}
                            onClick={() => remove(true)}
                        >
                            {formatMessage(messages.deleteOne)}
                        </button>
                    )}
                    {occurrence && (
                        <button
                            type='button'
                            className={am('btn', 'danger')}
                            disabled={saving}
                            onClick={() => remove(false)}
                        >
                            {formatMessage(repeating ? messages.deleteAll : messages.delete)}
                        </button>
                    )}
                </div>
            )}
        </form>
    );
}
