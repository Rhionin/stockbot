import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { ProvenanceBadge } from './ProvenanceBadge';
import type { ExternalSource } from '@stockbot/shared/types';

describe('ProvenanceBadge', () => {
  it('renders openfoodfacts with mapped name and accessible name', () => {
    render(
      <MantineProvider>
        <ProvenanceBadge externalSource="openfoodfacts" />
      </MantineProvider>,
    );

    const badge = screen.getByLabelText('Product data from Open Food Facts');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('Open Food Facts');
  });

  it('renders openproductsfacts with mapped name and accessible name', () => {
    render(
      <MantineProvider>
        <ProvenanceBadge externalSource="openproductsfacts" />
      </MantineProvider>,
    );

    const badge = screen.getByLabelText('Product data from Open Products Facts');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('Open Products Facts');
  });

  it('renders openbeautyfacts with mapped name and accessible name', () => {
    render(
      <MantineProvider>
        <ProvenanceBadge externalSource="openbeautyfacts" />
      </MantineProvider>,
    );

    const badge = screen.getByLabelText('Product data from Open Beauty Facts');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('Open Beauty Facts');
  });

  it('renders openpetfoodfacts with mapped name and accessible name', () => {
    render(
      <MantineProvider>
        <ProvenanceBadge externalSource="openpetfoodfacts" />
      </MantineProvider>,
    );

    const badge = screen.getByLabelText('Product data from Open Pet Food Facts');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('Open Pet Food Facts');
  });

  it('renders a quiet source label that is not a control', () => {
    render(
      <MantineProvider>
        <ProvenanceBadge externalSource="openfoodfacts" quiet />
      </MantineProvider>,
    );

    const label = screen.getByLabelText('Product data from Open Food Facts');
    expect(label).toHaveTextContent('Open Food Facts');
    expect(label.closest('.mantine-Badge-root')).toBeNull();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('renders nothing when externalSource is absent', () => {
    const { container } = render(
      <ProvenanceBadge />
    );

    expect(container.firstChild).toBeNull();
  });

  it('renders nothing when externalSource is unrecognized', () => {
    const { container } = render(
      <ProvenanceBadge externalSource={'unknowndb' as ExternalSource} />
    );

    expect(container.firstChild).toBeNull();
  });
});
