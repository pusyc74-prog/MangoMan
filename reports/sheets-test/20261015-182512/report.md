# Chargesheets test with real free models, 15 October 2026 17:44 UTC

**7 of 8 answers right.**

## 1. Loading

| File | Read |
| --- | --- |
| Product feed (Google Shopping) | 120 rows, 25 s; measures price, sale price; date -; about: (no model description) |
| Sales orders (India) | 230 rows, 16 s; measures Amount; date Order Date; about: This table contains 230 sales orders with details like date, customer, city, sales channel, amount, and payment method. |
| CRM leads | 150 rows, 25 s; measures Deal Value (INR); date Created; about: (no model description) |
| Ad report (months across) | 12 rows, 18 s; measures Jul Spend, Aug Spend, Sep Spend, Jul Clicks; date -; about: This table shows advertising campaign performance for Q3 (July-September) with spend, clicks, and conversions per month. |
| Inventory | 60 rows, 14 s; measures Qty on Hand, Avg Daily Sales, Unit Cost, Reorder Level; date Last Restock; about: This table tracks inventory levels, sales velocity, costs, and restocking details for 60 distinct products across Kitchen, Decor, and Bath categories. |

Google Sheet link: **works**. Title 'Example Spreadsheet', 30 rows; refresh changed: True 

## 2. Questions

| File | Question | Right | Missing | Unchecked numbers | Steps | Seconds | Answer |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Sales orders (India) | Which channel had the highest net sales in September 2026? | yes |  |  | 1 | 22 | Website had the highest net sales in September 2026, totaling ₹179,600. <br>`SELECT "Channel", ROUND(SUM("Amount"), 2) AS net_sales FROM kesar_sales WHERE strftime('%Y', "Order Date") = '2026' AND strftime('%m', "Order Date") = '09' GROUP BY "Channel" ORDER BY net_sales DESC LIMIT 1;` |
| Sales orders (India) | What were total net sales in August 2026? | yes |  |  | 1 | 186 | Total net sales in August 2026 were 552,300 INR. <br>`SELECT ROUND(SUM("Amount"), 2) AS total_net_sales FROM kesar_sales WHERE strftime('%Y-%m', "Order Date") = '2026-08'` |
| Sales orders (India) | And in July? | yes |  |  | 1 | 274 | Total net sales in July were ₹380,800. <br>`SELECT round(SUM("Amount"),2) AS total_sales FROM kesar_sales WHERE strftime('%Y-%m', "Order Date") = '2026-07'` |
| Sales orders (India) | Why did sales drop in September compared to August? | NO | no answer |  | 0 | 484 | MangoMan's router is not running: restart MangoMan <br>`` |
| Sales orders (India) | How many orders were returns (negative amounts)? | yes |  |  | 1 | 176 | 6 orders had a negative amount, so there were 6 returns in the data. <br>`SELECT COUNT(*) AS return_orders FROM kesar_sales WHERE "Amount" < 0` |
| Product feed (Google Shopping) | How many products are out of stock? | yes |  |  | 1 | 326 | 24 products are out of stock. <br>`SELECT COUNT(*) AS out_of_stock FROM pinecrest_feed WHERE LOWER("availability") = 'out of stock';` |
| Product feed (Google Shopping) | How many products have no GTIN? | yes |  |  | 1 | 316 | 27 products have no GTIN. <br>`SELECT COUNT(*) AS no_gtin FROM pinecrest_feed WHERE "gtin" IS NULL OR TRIM("gtin") = '';` |
| Product feed (Google Shopping) | What is the average price of the Equipment products? | yes |  |  | 1 | 250 | The average price of the Equipment products is $362.62. <br>`SELECT ROUND(AVG("price"), 2) AS avg_price FROM pinecrest_feed WHERE LOWER("product type") = LOWER('Outdoors > Equipment');` |
