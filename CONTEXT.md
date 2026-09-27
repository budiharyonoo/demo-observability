# Checkout Demo

This demo shows how telemetry helps a developer find the cause of a slow or failed Checkout.

## Language

**Checkout**:
A User's request to check a Product's Stock and create an Order for a Quantity.
_Avoid_: Purchase, payment

**User**:
The person identified by `user_id` in a Checkout.
_Avoid_: Customer, buyer

**Product**:
An item with a name, Price, and Stock.
_Avoid_: Article, SKU

**Quantity**:
The number of units of a Product requested in a Checkout.
_Avoid_: Count, amount

**Cart**:
The Product and Quantity that a User submits for Checkout.
_Avoid_: Basket, session

**Price**:
The value of one unit of a Product, expressed in whole Indonesian rupiah (IDR).
_Avoid_: Cost, rate

**Stock**:
The number of Product units available for a Checkout.
_Avoid_: Inventory, balance

**Order**:
The stored result of a successful Checkout, with its User, Product, Quantity, Price, total amount, and status.
_Avoid_: Transaction, purchase

**Scenario**:
A named demo condition that causes normal behavior, a delay, or a failure during Checkout.
_Avoid_: Mode, fault profile
