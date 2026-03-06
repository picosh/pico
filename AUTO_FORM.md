# Auto-Form Feature for pgs.sh

## Overview

The auto-form feature allows users to create HTML forms on their pgs.sh sites that automatically submit data to the pgs database. Form submissions can then be retrieved via the pgs CLI in JSON format.

## How It Works

1. Form submissions are automatically captured and stored in PostgreSQL
2. Users can retrieve form data via the pgs CLI

## Database Schema

```sql
CREATE TABLE form_entries (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  name VARCHAR(255) NOT NULL,
  data jsonb NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT form_entries_pkey PRIMARY KEY (id),
  CONSTRAINT fk_form_entries_users
    FOREIGN KEY(user_id)
    REFERENCES app_users(id)
    ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_form_entries_user ON form_entries(user_id);
CREATE INDEX IF NOT EXISTS idx_form_entries_name ON form_entries(name);
```

### Migration

Run the migration:
```bash
make latest
```

Or run all migrations:
```bash
make migrate
```

## HTML Form Example

```html
<form method="POST" action="/forms/example" data-pgs="true">
  <p>
    <label>Your Name: <input type="text" name="name" /></label>
  </p>
  <p>
    <label>Your Email: <input type="email" name="email" /></label>
  </p>
  <p>
    <label>Your Role: <select name="role[]" multiple>
      <option value="leader">Leader</option>
      <option value="follower">Follower</option>
    </select></label>
  </p>
  <p>
    <label>Message: <textarea name="message"></textarea></label>
  </p>
  <p>
    <button type="submit">Send</button>
  </p>
</form>
```

## CLI Commands

### List all form names for a user
```bash
ssh pgs.sh forms ls
```

### Get form submissions for a specific form
```bash
ssh pgs.sh forms show example
```

Output (JSON):
```json
[
  {
    "id": "uuid",
    "name": "contact",
    "data": {
      "name": "John Doe",
      "email": "john@example.com",
      "role": ["leader", "follower"],
      "message": "Hello!"
    },
    "created_at": "2026-03-05T12:00:00Z"
  }
]
```

### Delete all submissions for a form
```bash
ssh pgs.sh forms rm example --write
```

## Data Storage

- Form data is stored in PostgreSQL `form_entries` table
- Each submission is a JSON object with form field names as keys
- Data is associated with the user, not the project
- Form data is deleted when the user account is deleted (CASCADE)

## Implementation Details

### Database Methods

```go
InsertFormEntry(userID, name string, data map[string]interface{}) error
FindFormEntriesByUserAndName(userID, name string) ([]*db.FormEntry, error)
FindFormNamesByUser(userID string) ([]string, error)
RemoveFormEntriesByUserAndName(userID, name string) error
```

## More features

- Form validation and confirmation pages
- CSRF token in form and validated in post handler
