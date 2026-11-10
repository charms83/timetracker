-- Die Server-Version kennt keine Kunden; Projekte kommen aus den Quellen (Plane/Jira).
ALTER TABLE projects DROP COLUMN customer_id;
DROP TABLE customers;
