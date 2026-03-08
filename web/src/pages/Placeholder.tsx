export function PlaceholderPage({ title, icon }: { title: string; icon: string }) {
  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] text-center">
      <span className="text-6xl mb-4">{icon}</span>
      <h1 className="text-2xl font-bold text-text-primary mb-2">{title}</h1>
      <p className="text-text-secondary text-sm">
        This page is being migrated to the new React frontend.
        <br />
        Coming soon!
      </p>
    </div>
  )
}
