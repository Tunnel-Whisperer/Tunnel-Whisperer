# Application Templates

Application templates are reusable bundles of port mappings. If several users need the same set of ports (say, a web app on 80 plus its database on 5432), define the template once and load it when creating or editing users instead of typing the ports each time.

A template is just a name plus a list of `clientPort → serverPort` mappings — no credentials, no relay state.

## Managing Templates

```bash
# List templates and their mappings
tw server app list

# Create a template (interactive: name, then mappings)
tw server app create

# Edit a template's name and mappings
tw server app edit web-app

# Delete a template
tw server app delete web-app
```

In the dashboard, templates live under **Apps** in the nav bar.

## Using Templates for Users

Templates pre-fill user mappings in the dashboard:

- **Creating a user** — pick a template from the **Load from Application** dropdown on the create form; its mappings populate the form and can be adjusted before saving.
- **Editing a user** — the **Add from Application** dropdown appends a template's mappings to the ones being edited.

On the CLI, `tw server user create` takes mappings via `-m` or `--from <user>`; to reuse a template's ports, check them with `tw server app list` and pass them as `-m` flags.

!!! note "No retroactive changes"
    A template is copied into the user at creation/edit time. Editing or deleting a template does not affect users previously created from it — only new users or manual edits pick up the change.
