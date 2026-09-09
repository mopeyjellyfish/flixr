import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ settingsInventory: vi.fn().mockResolvedValue({ settings: [
  { key: 'library.films_root', category: 'Libraries', scope: 'server', default: '', environment: 'FLIXR_FILMS_ROOT', file_secret: '', persistence: 'database', restart: 'immediate', valid: 'existing directory', secret: false, advanced: false, value: '/media/films', source: 'saved', pending_value: '/mnt/films', pending_source: 'environment', mutable: false },
  { key: 'playback.lease_ttl', category: 'Playback', scope: 'server', default: '45s', environment: 'FLIXR_LEASE_TTL', file_secret: '', persistence: 'environment', restart: 'restart', valid: 'positive duration', secret: false, advanced: true, value: '45s', source: 'default', mutable: false },
] }), settingsImportPreview: vi.fn(), settingsImport: vi.fn() }));

vi.mock('../../api/client', () => ({ api: { settingsInventory: mocks.settingsInventory, roots: vi.fn(), settingsExport: vi.fn(), settingsImportPreview: mocks.settingsImportPreview, settingsImport: mocks.settingsImport } }));

import { SettingsPanel } from './SettingsPanel';

afterEach(() => { cleanup(); mocks.settingsImportPreview.mockReset(); mocks.settingsImport.mockReset(); });

describe('settings panel', () => {
  it('searches the basic inventory and reveals advanced entries explicitly', async () => {
    render(<SettingsPanel onNotice={() => undefined} />);
    expect(await screen.findByText('library.films_root')).toBeInTheDocument();
    expect(screen.getByText('/media/films')).toBeVisible();
    expect(screen.getByText(/environment requested \/mnt\/films/i)).toBeVisible();
    expect(screen.getByText(/managed by the server environment/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset library defaults' })).toBeDisabled();
    expect(screen.queryByText('playback.lease_ttl')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText(/show advanced settings/i));
    expect(screen.getByText('playback.lease_ttl')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/search settings/i), { target: { value: 'library' } });
    expect(screen.queryByText('playback.lease_ttl')).not.toBeInTheDocument();
  });

  it('invalidates a preview after its text changes and shows restart requirements', async () => {
    mocks.settingsImportPreview.mockResolvedValueOnce({ changes: { 'playback.segment_dir': { from: '/old', to: '/new' } }, restart_required: true });
    render(<SettingsPanel onNotice={() => undefined} />);
    await screen.findByText('library.films_root');
    fireEvent.click(screen.getByText('Import preview'));
    const input = screen.getByLabelText('Export JSON');
    fireEvent.change(input, { target: { value: '{"version":1,"settings":{"playback.segment_dir":"/new"}}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview changes' }));
    expect(await screen.findByText(/restart required after import/i)).toBeInTheDocument();
    fireEvent.change(input, { target: { value: '{"version":1,"settings":{"playback.segment_dir":"/other"}}' } });
    expect(screen.queryByRole('button', { name: 'Apply previewed changes' })).not.toBeInTheDocument();
    expect(mocks.settingsImport).not.toHaveBeenCalled();
  });
});
