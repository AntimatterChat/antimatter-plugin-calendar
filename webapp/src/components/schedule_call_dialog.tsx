// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import ReactDOM from 'react-dom';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';
import * as client from '../client';
import {onScheduleCallDialog, type ScheduleCallTarget} from '../schedule_call';
import {dateInput, parseLocal, timeInput} from '../utils';
import {isFusionUI} from '../web_ui';

import {fusionLayer} from './fusion/fusion_ui';
import {messages} from './messages';

const DEFAULT_LEAD_MINUTES = 15;
const DEFAULT_DURATION_MINUTES = 30;

// roundToQuarterHour returns the next quarter hour at or after a date.
function roundToQuarterHour(date: Date): Date {
    const d = new Date(date);
    d.setSeconds(0, 0);
    const remainder = d.getMinutes() % 15;
    if (remainder > 0) {
        d.setMinutes(d.getMinutes() + (15 - remainder));
    }
    return d;
}

// ScheduleCallDialogRoot is the host component of the "Schedule a call" dialog: registered once
// (registerRootComponent), it renders nothing until the channel menu, or a meeting post's own
// Edit button, opens it.
export default function ScheduleCallDialogRoot() {
    const [target, setTarget] = useState<ScheduleCallTarget | null>(null);
    useEffect(() => onScheduleCallDialog(setTarget), []);

    if (!target) {
        return null;
    }
    const layer = (isFusionUI() && fusionLayer()) || document.body;
    return ReactDOM.createPortal(
        <div className={own('root', {fusion: isFusionUI(), classic: !isFusionUI()})}>
            <ScheduleCallForm
                key={target.channelId + (target.meeting?.id || '')}
                target={target}
                onDone={() => setTarget(null)}
            />
        </div>,
        layer,
    );
}

type FormProps = {
    target: ScheduleCallTarget;
    onDone: () => void;
};

function ScheduleCallForm({target, onDone}: FormProps) {
    const {formatMessage} = useIntl();
    const {meeting} = target;

    const initialStart = meeting ? new Date(meeting.start) : roundToQuarterHour(new Date(Date.now() + DEFAULT_LEAD_MINUTES * 60000));
    const initialEnd = meeting ? new Date(meeting.end) : new Date(initialStart.getTime() + DEFAULT_DURATION_MINUTES * 60000);

    const [summary, setSummary] = useState(meeting?.summary || '');
    const [description, setDescription] = useState(meeting?.description || '');
    const [startDate, setStartDate] = useState(dateInput(initialStart));
    const [startTime, setStartTime] = useState(timeInput(initialStart));
    const [endDate, setEndDate] = useState(dateInput(initialEnd));
    const [endTime, setEndTime] = useState(timeInput(initialEnd));
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');

    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.stopPropagation();
                onDone();
            }
        };
        document.addEventListener('keydown', onKeyDown, true);
        return () => document.removeEventListener('keydown', onKeyDown, true);
    }, [onDone]);

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        const start = parseLocal(startDate, startTime);
        const end = parseLocal(endDate, endTime);
        if (!(end.getTime() > start.getTime())) {
            setError(formatMessage(messages.scheduleCallEndAfterStart));
            return;
        }

        setSaving(true);
        setError('');
        const input = {summary: summary.trim(), description: description.trim(), start: start.getTime(), end: end.getTime()};
        try {
            if (meeting) {
                await client.updateMeeting(meeting.id, input);
            } else {
                await client.scheduleCall(target.channelId, input);
            }
            onDone();
        } catch (err) {
            setError((err as Error).message);
            setSaving(false);
        }
    };

    return (
        <div
            className={am('scrim')}
            onMouseDown={(e) => {
                if (e.target === e.currentTarget) {
                    onDone();
                }
            }}
        >
            <form
                className={am('modal', 'small')}
                role='dialog'
                aria-modal='true'
                aria-label={formatMessage(meeting ? messages.editCallTitle : messages.scheduleCallTitle)}
                onSubmit={submit}
            >
                <div className={am('pane')}>
                    <h2>{formatMessage(meeting ? messages.editCallTitle : messages.scheduleCallTitle)}</h2>
                    <p className={am('lead')}>{formatMessage(messages.scheduleCallLead)}</p>
                    <label
                        className={am('field')}
                        htmlFor='amc-call-summary'
                    >
                        <span>{formatMessage(messages.summary)}</span>
                        <input
                            id='amc-call-summary'
                            type='text'
                            required={true}
                            autoFocus={true}
                            maxLength={1000}
                            placeholder={formatMessage(messages.scheduleCallPlaceholder)}
                            value={summary}
                            onChange={(e) => setSummary(e.target.value)}
                        />
                    </label>
                    <div className={own('field-row')}>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-start-date'
                        >
                            <span>{formatMessage(messages.starts)}</span>
                            <input
                                id='amc-call-start-date'
                                type='date'
                                className={own('input')}
                                required={true}
                                value={startDate}
                                onChange={(e) => {
                                    const shift = parseLocal(e.target.value).getTime() - parseLocal(startDate).getTime();
                                    setEndDate(dateInput(new Date(parseLocal(endDate).getTime() + shift)));
                                    setStartDate(e.target.value);
                                }}
                            />
                        </label>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-start-time'
                        >
                            <span>{' '}</span>
                            <input
                                id='amc-call-start-time'
                                type='time'
                                className={own('input')}
                                required={true}
                                value={startTime}
                                onChange={(e) => setStartTime(e.target.value)}
                            />
                        </label>
                    </div>
                    <div className={own('field-row')}>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-end-date'
                        >
                            <span>{formatMessage(messages.ends)}</span>
                            <input
                                id='amc-call-end-date'
                                type='date'
                                className={own('input')}
                                required={true}
                                min={startDate}
                                value={endDate}
                                onChange={(e) => setEndDate(e.target.value)}
                            />
                        </label>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-end-time'
                        >
                            <span>{' '}</span>
                            <input
                                id='amc-call-end-time'
                                type='time'
                                className={own('input')}
                                required={true}
                                value={endTime}
                                onChange={(e) => setEndTime(e.target.value)}
                            />
                        </label>
                    </div>
                    <label
                        className={am('field')}
                        htmlFor='amc-call-description'
                    >
                        <span>{formatMessage(messages.description)}</span>
                        <textarea
                            id='amc-call-description'
                            maxLength={4000}
                            value={description}
                            onChange={(e) => setDescription(e.target.value)}
                        />
                    </label>
                    {error && <div className={own('error')}>{error}</div>}
                    <div className={am('modal-actions')}>
                        <button
                            type='button'
                            className={am('btn')}
                            onClick={onDone}
                        >
                            {formatMessage(messages.cancel)}
                        </button>
                        <button
                            type='submit'
                            className={am('btn', 'primary')}
                            disabled={saving}
                        >
                            {formatMessage(saving ? messages.saving : (meeting ? messages.save : messages.scheduleCallSubmit))}
                        </button>
                    </div>
                </div>
            </form>
        </div>
    );
}
