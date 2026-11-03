/** Swappable Kroger site labels (UX spec §12.2 k.*). */
export const krogerLabels = {
  urlHome: 'https://developer.kroger.com/',
  s1Account:
    'This is a separate account from your store\'s grocery login.',
  s2Body:
    'Open "Manage", then "Apps", then "Register". Choose "Production" for the environment.',
  fName: 'Application Name',
  fDesc: 'Application Description',
  fEmail: 'Support Email Address',
  fRedirect: 'Web Redirect URL',
  s4Body: 'In "API Products", tick "Cart" and "Products". Tick "Locations" too if you see it.',
  boxCart: 'Cart',
  boxProducts: 'Products',
  boxLocations: 'Locations',
  optProduction: 'Production',
  s5Body:
    'Press "Register Application". Kroger shows your two keys: "Client ID" and "Client Secret".',
  fId: 'App ID',
  fIdHint: 'Kroger calls this "Client ID".',
  fSecret: 'App secret',
  fSecretHint: 'Kroger calls this "Client Secret".',
} as const;
