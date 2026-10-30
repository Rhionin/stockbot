export function KitPrintFooter({ printableName }: { printableName: string }) {
  return (
    <footer className="kit-print-footer">
      <p className="kit-print-footer-instruction">
        Print at Actual size (100%), not Fit to page.
      </p>
      <div className="kit-print-ruler-wrap">
        <div className="kit-print-ruler" aria-hidden="true" />
        <p className="kit-print-ruler-caption">
          This line should measure 50 mm (about 2 inches). If it&apos;s shorter, print again at 100%.
        </p>
      </div>
      <p className="kit-print-footer-meta">
        Stockbot · {printableName} · v1 (2026-10)
      </p>
    </footer>
  );
}
