import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ settingsInventory: vi.fn().mockResolvedValue({ settings: [
  { key: 'library.films_root', category: 'Libraries', scope: 'server', default: '', environment: 'FLIXR_FILMS_ROOT', file_secret: '', persistence: 'database', restart: 'immediate', valid: 'existing directory', secret: false, advanced: false, value: '/media/films', source: 'environment', mutable: false },
  { key: 'playback.lease_ttl', category: 'Playback', scope: 'server', default: '45s', environment: 'FLIXR_LEASE_TTL', file_secret: '', persistence: 'environment', restart: 'restart', valid: 'positive duration', secret: false, advanced: true, value: '45s', source: 'default', mutable: false },
] }) }));

vi.mock('../../api/client', () => ({ api: { settingsInventory: mocks.settingsInventory, roots: vi.fn(), settingsExport: vi.fn(), settingsImportPreview: vi.fn(), settingsImport: vi.fn() } }));

import { SettingsPanel } from './SettingsPanel';

describe('settings panel', () => {
  it('searches the basic inventory and reveals advanced entries explicitly', async () => {
    render(<SettingsPanel onNotice={() => undefined} />);
    expect(await screen.findByText('library.films_root')).toBeInTheDocument();
    expect(screen.getByText(/managed by the server environment/i)).toBeInTheDocument();
    expect(screen.queryByText('playback.lease_ttl')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText(/show advanced settings/i));
    expect(screen.getByText('playback.lease_ttl')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/search settings/i), { target: { value: 'library' } });
    expect(screen.queryByText('playback.lease_ttl')).not.toBeInTheDocument();
  });
});
