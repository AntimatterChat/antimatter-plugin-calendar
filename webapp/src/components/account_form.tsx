// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';
import * as client from '../client';
import type {Account} from '../client';

import {messages} from './messages';

const REMIND_CHOICES = [0, 5, 10, 15, 30, 60];

type Props = {
    account: Account | null;
    remindersEnabled: boolean;
    onSaved: (account: Account) => void;
    onCancel: (() => void) | null;
    onDisconnected: () => void;
};

// AccountForm connects a calendar server, or changes its settings.
export default function AccountForm({account, remindersEnabled, onSaved, onCancel, onDisconnected}: Props) {
    const {formatMessage} = useIntl();
    const [url, setURL] = useState(account?.url || '');
    const [username, setUsername] = useState(account?.username || '');
    const [password, setPassword] = useState('');
    const [remind, setRemind] = useState(account ? account.remind_minutes : 10);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');

    let submitLabel = account ? messages.save : messages.connect;
    if (saving) {
        submitLabel = messages.connecting;
    }

    const save = async (e: React.FormEvent) => {
        e.preventDefault();
        setSaving(true);
        setError('');
        try {
            onSaved(await client.saveAccount({url: url.trim(), username: username.trim(), password, remind_minutes: remind}));
        } catch (err) {
            setError((err as Error).message);
        } finally {
            setSaving(false);
        }
    };

    const disconnect = async () => {
        // eslint-disable-next-line no-alert
        if (!window.confirm(formatMessage(messages.disconnectConfirm))) {
            return;
        }
        try {
            await client.deleteAccount();
            onDisconnected();
        } catch (err) {
            setError((err as Error).message);
        }
    };

    return (
        <form
            className={am('app-panel')}
            onSubmit={save}
        >
            <div className={own('form-head')}>
                <h3>{formatMessage(account ? messages.settingsTitle : messages.setupTitle)}</h3>
                {!account && <p>{formatMessage(messages.setupIntro)}</p>}
            </div>
            <label
                className={am('field')}
                htmlFor='amc-url'
            >
                <span>{formatMessage(messages.serverURL)}</span>
                <input
                    id='amc-url'
                    type='text'
                    inputMode='url'
                    required={true}
                    value={url}
                    placeholder='https://cloud.example.com/remote.php/dav'
                    spellCheck={false}
                    autoComplete='url'
                    onChange={(e) => setURL(e.target.value)}
                />
            </label>
            <label
                className={am('field')}
                htmlFor='amc-username'
            >
                <span>{formatMessage(messages.username)}</span>
                <input
                    id='amc-username'
                    type='text'
                    required={true}
                    value={username}
                    spellCheck={false}
                    autoComplete='username'
                    onChange={(e) => setUsername(e.target.value)}
                />
            </label>
            <label
                className={am('field')}
                htmlFor='amc-password'
            >
                <span>{formatMessage(messages.password)}</span>
                <input
                    id='amc-password'
                    type='password'
                    className={own('input')}
                    required={!account}
                    value={password}
                    autoComplete='new-password'
                    onChange={(e) => setPassword(e.target.value)}
                />
                {account && <small>{formatMessage(messages.passwordKeep)}</small>}
            </label>
            <label
                className={am('field')}
                htmlFor='amc-remind'
            >
                <span>{formatMessage(messages.remind)}</span>
                <select
                    id='amc-remind'
                    value={remind}
                    disabled={!remindersEnabled}
                    onChange={(e) => setRemind(Number(e.target.value))}
                >
                    {REMIND_CHOICES.map((minutes) => (
                        <option
                            key={minutes}
                            value={minutes}
                        >
                            {minutes === 0 ? formatMessage(messages.alarmNone) : formatMessage(messages.alarmMinutes, {count: minutes})}
                        </option>
                    ))}
                </select>
                <small>{formatMessage(remindersEnabled ? messages.remindHelp : messages.remindersOff)}</small>
            </label>
            {error && <div className={own('error')}>{error}</div>}
            <small className={own('note')}>{formatMessage(messages.passwordStored)}</small>
            <div className={own('actions')}>
                <button
                    type='submit'
                    className={am('btn', 'primary')}
                    disabled={saving}
                >
                    {formatMessage(submitLabel)}
                </button>
                {onCancel && (
                    <button
                        type='button'
                        className={am('btn')}
                        onClick={onCancel}
                    >
                        {formatMessage(messages.cancel)}
                    </button>
                )}
                <span className={am('grow')}/>
                {account && (
                    <button
                        type='button'
                        className={am('btn', 'danger')}
                        onClick={disconnect}
                    >
                        {formatMessage(messages.disconnect)}
                    </button>
                )}
            </div>
        </form>
    );
}
