import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { Wordmark } from './Wordmark';

afterEach(cleanup);

it('renders the accepted FlixR identity through one shared seam', () => {
  render(<Wordmark />);
  const wordmark = screen.getByText('Flix').closest('.wordmark');
  expect(wordmark).toHaveTextContent('FlixR');
  expect(wordmark?.querySelector('i')).toHaveTextContent('R');
});
